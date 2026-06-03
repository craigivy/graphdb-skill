package analysis

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"graphdb/internal/graph"
)

type GolangParser struct{}

func init() {
	RegisterParser(".go", &GolangParser{})
}

func (p *GolangParser) Parse(filePath string, content []byte) ([]*graph.Node, []*graph.Edge, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, nil, err
	}
	defer tree.Close()

	var nodes []*graph.Node
	var edges []*graph.Edge

	// 1. Extract Package Name
	packageName := "main"
	qPkg, err := sitter.NewQuery([]byte(`(package_clause (package_identifier) @package.name)`), golang.GetLanguage())
	if err == nil {
		qcPkg := sitter.NewQueryCursor()
		qcPkg.Exec(qPkg, tree.RootNode())
		if m, ok := qcPkg.NextMatch(); ok {
			for _, c := range m.Captures {
				packageName = c.Node.Content(content)
			}
		}
		qcPkg.Close()
		qPkg.Close()
	}

	// 2. Extract Imports
	imports := make(map[string]string)
	qImp, err := sitter.NewQuery([]byte(`
		(import_spec
			name: (package_identifier)? @import.alias
			path: [
				(interpreted_string_literal)
				(raw_string_literal)
			] @import.path)
	`), golang.GetLanguage())
	if err == nil {
		qcImp := sitter.NewQueryCursor()
		qcImp.Exec(qImp, tree.RootNode())
		for {
			m, ok := qcImp.NextMatch()
			if !ok {
				break
			}
			var alias string
			var path string
			for _, c := range m.Captures {
				name := qImp.CaptureNameForId(c.Index)
				if name == "import.alias" {
					alias = c.Node.Content(content)
				} else if name == "import.path" {
					path = strings.Trim(c.Node.Content(content), "\"`")
				}
			}
			if path != "" {
				if alias != "" {
					imports[alias] = path
				} else {
					parts := strings.Split(path, "/")
					defaultName := parts[len(parts)-1]
					imports[defaultName] = path
				}
			}
		}
		qcImp.Close()
		qImp.Close()
	}

	// 3. Extract Definitions (Structs, Interfaces, Fields, Functions, Methods)
	defQueryStr := `
		(type_spec
			name: (type_identifier) @struct.name
			type: (struct_type)) @struct.def

		(type_spec
			name: (type_identifier) @interface.name
			type: (interface_type)) @interface.def

		(function_declaration
			name: (identifier) @function.name) @function.def

		(method_declaration
			receiver: (parameter_list
				(parameter_declaration
					type: (_) @method.receiver))
			name: (_) @method.name) @method.def
	`
	qDef, err := sitter.NewQuery([]byte(defQueryStr), golang.GetLanguage())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid definitions query: %w", err)
	}
	defer qDef.Close()

	qcDef := sitter.NewQueryCursor()
	defer qcDef.Close()
	qcDef.Exec(qDef, tree.RootNode())

	for {
		m, ok := qcDef.NextMatch()
		if !ok {
			break
		}

		var structNameNode *sitter.Node
		var structDefNode *sitter.Node
		var interfaceNameNode *sitter.Node
		var interfaceDefNode *sitter.Node
		var funcNameNode *sitter.Node
		var funcDefNode *sitter.Node
		var methodNameNode *sitter.Node
		var methodDefNode *sitter.Node
		var receiverNode *sitter.Node

		for _, c := range m.Captures {
			captureName := qDef.CaptureNameForId(c.Index)
			switch captureName {
			case "struct.name":
				structNameNode = c.Node
			case "struct.def":
				structDefNode = c.Node
			case "interface.name":
				interfaceNameNode = c.Node
			case "interface.def":
				interfaceDefNode = c.Node
			case "function.name":
				funcNameNode = c.Node
			case "function.def":
				funcDefNode = c.Node
			case "method.name":
				methodNameNode = c.Node
			case "method.def":
				methodDefNode = c.Node
			case "method.receiver":
				receiverNode = c.Node
			}
		}

		// --- Struct Processing ---
		if structNameNode != nil && structDefNode != nil {
			structName := structNameNode.Content(content)
			structFqn := fmt.Sprintf("%s:%s.%s", filePath, packageName, structName)
			structID := GenerateNodeID("Class", structFqn, "")

			nodes = append(nodes, &graph.Node{
				ID:    structID,
				Label: "Class",
				Properties: map[string]interface{}{
					"name":       structName,
					"fqn":        structFqn,
					"file":       filePath,
					"start_line": int(structDefNode.StartPoint().Row + 1),
					"end_line":   int(structDefNode.EndPoint().Row + 1),
				},
			})

			// Parse Struct Fields
			fields := findGoFieldDeclarations(structDefNode)
			for _, fieldNode := range fields {
				fieldNames := extractGoFieldNames(fieldNode, content)
				if len(fieldNames) > 0 {
					for _, fieldName := range fieldNames {
						fieldFqn := fmt.Sprintf("%s:%s.%s.%s", filePath, packageName, structName, fieldName)
						fieldID := GenerateNodeID("Field", fieldFqn, "")

						nodes = append(nodes, &graph.Node{
							ID:    fieldID,
							Label: "Field",
							Properties: map[string]interface{}{
								"name":       fieldName,
								"fqn":        fieldFqn,
								"file":       filePath,
								"start_line": int(fieldNode.StartPoint().Row + 1),
								"end_line":   int(fieldNode.EndPoint().Row + 1),
							},
						})

						// Struct DEFINES Field
						edges = append(edges, &graph.Edge{
							SourceID: structID,
							TargetID: fieldID,
							Type:     "DEFINES",
						})
					}
				} else {
					// Anonymous / Embedded field
					embeddedName := extractEmbeddedFieldName(fieldNode, content)
					if embeddedName != "" {
						embeddedFqn := fmt.Sprintf("%s:%s.%s", filePath, packageName, embeddedName)
						// If the embedded name resides in another package
						if strings.Contains(embeddedName, ".") {
							parts := strings.Split(embeddedName, ".")
							pkg := parts[0]
							sym := parts[1]
							if resolved, ok := imports[pkg]; ok {
								embeddedFqn = fmt.Sprintf("%s:%s", resolved, sym)
							} else {
								embeddedFqn = embeddedName
							}
						}
						embeddedID := GenerateNodeID("Class", embeddedFqn, "")

						// Struct EXTENDS Embedded Class
						edges = append(edges, &graph.Edge{
							SourceID: structID,
							TargetID: embeddedID,
							Type:     "EXTENDS",
						})
					}
				}
			}
		}

		// --- Interface Processing ---
		if interfaceNameNode != nil && interfaceDefNode != nil {
			interfaceName := interfaceNameNode.Content(content)
			interfaceFqn := fmt.Sprintf("%s:%s.%s", filePath, packageName, interfaceName)
			interfaceID := GenerateNodeID("Interface", interfaceFqn, "")

			nodes = append(nodes, &graph.Node{
				ID:    interfaceID,
				Label: "Interface",
				Properties: map[string]interface{}{
					"name":       interfaceName,
					"fqn":        interfaceFqn,
					"file":       filePath,
					"start_line": int(interfaceDefNode.StartPoint().Row + 1),
					"end_line":   int(interfaceDefNode.EndPoint().Row + 1),
				},
			})

			// Parse Interface Methods & Embedded Interfaces
			methods, embeds := parseGoInterfaceContent(interfaceDefNode, content)
			for _, mName := range methods {
				methodFqn := fmt.Sprintf("%s:%s.%s.%s", filePath, packageName, interfaceName, mName)
				methodID := GenerateNodeID("Function", methodFqn, "()")

				nodes = append(nodes, &graph.Node{
					ID:    methodID,
					Label: "Function",
					Properties: map[string]interface{}{
						"name":       mName,
						"fqn":        methodFqn,
						"file":       filePath,
						"start_line": int(interfaceDefNode.StartPoint().Row + 1),
						"end_line":   int(interfaceDefNode.EndPoint().Row + 1),
					},
				})

				// Interface DEFINES Method (Function)
				edges = append(edges, &graph.Edge{
					SourceID: interfaceID,
					TargetID: methodID,
					Type:     "DEFINES",
				})
			}

			for _, embeddedName := range embeds {
				embeddedFqn := fmt.Sprintf("%s:%s.%s", filePath, packageName, embeddedName)
				if strings.Contains(embeddedName, ".") {
					parts := strings.Split(embeddedName, ".")
					pkg := parts[0]
					sym := parts[1]
					if resolved, ok := imports[pkg]; ok {
						embeddedFqn = fmt.Sprintf("%s:%s", resolved, sym)
					} else {
						embeddedFqn = embeddedName
					}
				}
				embeddedID := GenerateNodeID("Interface", embeddedFqn, "")

				// Interface EXTENDS Embedded Interface
				edges = append(edges, &graph.Edge{
					SourceID: interfaceID,
					TargetID: embeddedID,
					Type:     "EXTENDS",
				})
			}
		}

		// --- Function Processing ---
		if funcNameNode != nil && funcDefNode != nil {
			funcName := funcNameNode.Content(content)
			funcFqn := fmt.Sprintf("%s:%s.%s", filePath, packageName, funcName)
			funcID := GenerateNodeID("Function", funcFqn, "()")

			nodes = append(nodes, &graph.Node{
				ID:    funcID,
				Label: "Function",
				Properties: map[string]interface{}{
					"name":       funcName,
					"fqn":        funcFqn,
					"file":       filePath,
					"start_line": int(funcDefNode.StartPoint().Row + 1),
					"end_line":   int(funcDefNode.EndPoint().Row + 1),
					"signature":  "()",
				},
			})
		}

		// --- Method Processing ---
		if methodNameNode != nil && methodDefNode != nil && receiverNode != nil {
			methodName := methodNameNode.Content(content)
			receiverRaw := receiverNode.Content(content)
			receiverType := strings.TrimSpace(receiverRaw)
			receiverType = strings.TrimPrefix(receiverType, "*")
			receiverType = strings.Trim(receiverType, "()")

			methodFqn := fmt.Sprintf("%s:%s.%s.%s", filePath, packageName, receiverType, methodName)
			methodID := GenerateNodeID("Function", methodFqn, "()")

			nodes = append(nodes, &graph.Node{
				ID:    methodID,
				Label: "Function",
				Properties: map[string]interface{}{
					"name":       methodName,
					"fqn":        methodFqn,
					"file":       filePath,
					"start_line": int(methodDefNode.StartPoint().Row + 1),
					"end_line":   int(methodDefNode.EndPoint().Row + 1),
					"signature":  "()",
				},
			})

			// Struct Class DEFINES Method Function
			classFqn := fmt.Sprintf("%s:%s.%s", filePath, packageName, receiverType)
			classID := GenerateNodeID("Class", classFqn, "")
			edges = append(edges, &graph.Edge{
				SourceID: classID,
				TargetID: methodID,
				Type:     "DEFINES",
			})
		}
	}

	// 4. Extract Calls (Function and Method Calls)
	callQueryStr := `
		(call_expression
			function: (identifier) @call.target) @call.site

		(call_expression
			function: (selector_expression
				operand: (_) @call.operand
				field: (field_identifier) @call.target)) @call.site
	`
	qCall, err := sitter.NewQuery([]byte(callQueryStr), golang.GetLanguage())
	if err == nil {
		defer qCall.Close()
		qcCall := sitter.NewQueryCursor()
		defer qcCall.Close()
		qcCall.Exec(qCall, tree.RootNode())

		for {
			m, ok := qcCall.NextMatch()
			if !ok {
				break
			}

			var callSite *sitter.Node
			var callTarget *sitter.Node
			var callOperand *sitter.Node

			for _, c := range m.Captures {
				name := qCall.CaptureNameForId(c.Index)
				if name == "call.site" {
					callSite = c.Node
				} else if name == "call.target" {
					callTarget = c.Node
				} else if name == "call.operand" {
					callOperand = c.Node
				}
			}

			if callSite != nil && callTarget != nil {
				// Find Enclosing Function/Method
				enclosingFunc, recType := findEnclosingGoFunctionAndReceiver(callSite, content)
				if enclosingFunc != "" {
					var sourceFqn string
					if recType != "" {
						sourceFqn = fmt.Sprintf("%s:%s.%s.%s", filePath, packageName, recType, enclosingFunc)
					} else {
						sourceFqn = fmt.Sprintf("%s:%s.%s", filePath, packageName, enclosingFunc)
					}
					sourceID := GenerateNodeID("Function", sourceFqn, "()")

					targetName := callTarget.Content(content)
					var targetFqn string
					var targetID string

					if callOperand != nil {
						operandName := callOperand.Content(content)
						// Check if operand is receiver
						if operandName == "self" || operandName == "this" || (recType != "" && isGoReceiverName(callSite, operandName, content)) {
							targetFqn = fmt.Sprintf("%s:%s.%s.%s", filePath, packageName, recType, targetName)
							targetID = GenerateNodeID("Function", targetFqn, "()")
						} else if resolvedPath, ok := imports[operandName]; ok {
							// Imported Package Call
							// Standard packages or workspace packages
							if isStandardGoPackage(resolvedPath) {
								targetFqn = fmt.Sprintf("%s.%s", resolvedPath, targetName)
								targetID = GenerateNodeID("Function", targetFqn, "")
							} else {
								targetFqn = fmt.Sprintf("%s:%s.%s", resolvedPath, operandName, targetName)
								targetID = GenerateNodeID("Function", targetFqn, "")
							}
						} else {
							// Fallback local member/operand call
							targetFqn = fmt.Sprintf("%s:%s.%s.%s", filePath, packageName, operandName, targetName)
							targetID = GenerateNodeID("Function", targetFqn, "()")
						}
					} else {
						// Local/Package function call
						targetFqn = fmt.Sprintf("%s:%s.%s", filePath, packageName, targetName)
						targetID = GenerateNodeID("Function", targetFqn, "()")
					}

					edges = append(edges, &graph.Edge{
						SourceID: sourceID,
						TargetID: targetID,
						Type:     "CALLS",
					})
				}
			}
		}
	}

	return nodes, edges, nil
}

// --- Helper Functions ---

func findGoFieldDeclarations(node *sitter.Node) []*sitter.Node {
	var fields []*sitter.Node
	var traverse func(n *sitter.Node)
	traverse = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "field_declaration" {
			fields = append(fields, n)
			return
		}
		count := n.ChildCount()
		for i := 0; i < int(count); i++ {
			traverse(n.Child(i))
		}
	}
	traverse(node)
	return fields
}

func extractGoFieldNames(node *sitter.Node, content []byte) []string {
	var names []string
	count := node.ChildCount()
	for i := 0; i < int(count); i++ {
		child := node.Child(i)
		if child.Type() == "field_identifier" {
			names = append(names, child.Content(content))
		}
	}
	return names
}

func extractEmbeddedFieldName(node *sitter.Node, content []byte) string {
	// Look for a type_identifier or pointer_type -> type_identifier
	var typeNode *sitter.Node
	var traverse func(n *sitter.Node)
	traverse = func(n *sitter.Node) {
		if n == nil || typeNode != nil {
			return
		}
		if n.Type() == "type_identifier" {
			typeNode = n
			return
		}
		count := n.ChildCount()
		for i := 0; i < int(count); i++ {
			traverse(n.Child(i))
		}
	}
	traverse(node)

	if typeNode != nil {
		return typeNode.Content(content)
	}
	return ""
}

func parseGoInterfaceContent(node *sitter.Node, content []byte) ([]string, []string) {
	var methods []string
	var embeds []string

	var traverse func(n *sitter.Node)
	traverse = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "method_spec" {
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				methods = append(methods, nameNode.Content(content))
			}
			return
		}
		if n.Type() == "type_identifier" {
			embeds = append(embeds, n.Content(content))
			return
		}
		count := n.ChildCount()
		for i := 0; i < int(count); i++ {
			traverse(n.Child(i))
		}
	}
	traverse(node)

	return methods, embeds
}

func findEnclosingGoFunctionAndReceiver(node *sitter.Node, content []byte) (string, string) {
	var functionName string
	var receiverType string

	curr := node.Parent()
	for curr != nil {
		if curr.Type() == "function_declaration" {
			nameNode := curr.ChildByFieldName("name")
			if nameNode != nil {
				functionName = nameNode.Content(content)
				break
			}
		}
		if curr.Type() == "method_declaration" {
			nameNode := curr.ChildByFieldName("name")
			if nameNode != nil {
				functionName = nameNode.Content(content)
			}
			recNode := curr.ChildByFieldName("receiver")
			if recNode != nil {
				raw := recNode.Content(content)
				clean := strings.TrimSpace(raw)
				clean = strings.TrimPrefix(clean, "*")
				clean = strings.Trim(clean, "()")
				// Take the last part (the type name) if it has a variable name: e.g. "(s *MyStruct)" -> "MyStruct"
				parts := strings.Fields(clean)
				if len(parts) > 0 {
					last := parts[len(parts)-1]
					last = strings.TrimPrefix(last, "*")
					receiverType = last
				}
			}
			break
		}
		curr = curr.Parent()
	}
	return functionName, receiverType
}

func isGoReceiverName(node *sitter.Node, name string, content []byte) bool {
	curr := node.Parent()
	for curr != nil {
		if curr.Type() == "method_declaration" {
			recNode := curr.ChildByFieldName("receiver")
			if recNode != nil {
				// E.g. "(s *MyStruct)"
				count := recNode.ChildCount()
				for i := 0; i < int(count); i++ {
					child := recNode.Child(i)
					if child.Type() == "parameter_declaration" {
						nameNode := child.ChildByFieldName("name")
						if nameNode != nil && nameNode.Content(content) == name {
							return true
						}
					}
				}
			}
			break
		}
		curr = curr.Parent()
	}
	return false
}

func isStandardGoPackage(path string) bool {
	// Standard Go packages are typically single tokens without slashes, or prefix folders like "archive", "compress", "container", "crypto", "database", "encoding", "go", "hash", "html", "image", "index", "io", "log", "math", "mime", "net", "os", "path", "reflect", "regexp", "runtime", "sort", "strconv", "strings", "sync", "syscall", "testing", "text", "time", "unicode", "unsafe"
	if !strings.Contains(path, ".") {
		return true
	}
	return false
}
