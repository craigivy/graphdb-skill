package analysis

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"graphdb/internal/graph"
)

type JavaScriptParser struct{}

func init() {
	RegisterParser(".js", &JavaScriptParser{})
}

func (p *JavaScriptParser) Parse(filePath string, content []byte) ([]*graph.Node, []*graph.Edge, error) {
	filePath = strings.ReplaceAll(filePath, "\\", "/")
	parser := sitter.NewParser()
	parser.SetLanguage(javascript.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, nil, err
	}
	defer tree.Close()

	var nodes []*graph.Node
	var edges []*graph.Edge

	imports := make(map[string]string)
	classFields := make(map[string]map[string]string) // ClassName -> FieldName -> Target

	// 1. Import / Require Query
	importQueryStr := `
		(import_statement
			(import_clause 
				(named_imports 
					(import_specifier) @import.specifier
				)
			)
			(string) @import.source
		)
		(import_statement
			(import_clause 
				(identifier) @import.default
			)
			(string) @import.source
		)
		(import_statement
			(import_clause 
				(namespace_import (identifier) @import.namespace)
			)
			(string) @import.source
		)
		(variable_declarator
			name: (identifier) @import.default
			value: (call_expression
				function: (identifier) @require.func
				arguments: (arguments (string) @import.source)
			)
		)
		(variable_declarator
			name: (object_pattern) @import.obj
			value: (call_expression
				function: (identifier) @require.func
				arguments: (arguments (string) @import.source)
			)
		)
	`
	qImport, err := sitter.NewQuery([]byte(importQueryStr), javascript.GetLanguage())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid import query: %w", err)
	}
	defer qImport.Close()

	qcImport := sitter.NewQueryCursor()
	defer qcImport.Close()
	qcImport.Exec(qImport, tree.RootNode())

	for {
		m, ok := qcImport.NextMatch()
		if !ok {
			break
		}

		var sourcePath string
		var defaultNode, specifierNode, namespaceNode, requireFuncNode, objPatternNode *sitter.Node

		for _, c := range m.Captures {
			name := qImport.CaptureNameForId(c.Index)
			if name == "import.source" {
				sourcePath = c.Node.Content(content)
				sourcePath = strings.Trim(sourcePath, "\"'`")
			} else if name == "import.default" {
				defaultNode = c.Node
			} else if name == "import.specifier" {
				specifierNode = c.Node
			} else if name == "import.namespace" {
				namespaceNode = c.Node
			} else if name == "require.func" {
				requireFuncNode = c.Node
			} else if name == "import.obj" {
				objPatternNode = c.Node
			}
		}

		if sourcePath != "" {
			if requireFuncNode != nil && requireFuncNode.Content(content) != "require" {
				continue
			}

			resolvedPath := resolveJSPath(filePath, sourcePath)

			if defaultNode != nil {
				localName := defaultNode.Content(content)
				imports[localName] = fmt.Sprintf("%s:default", resolvedPath)
			}

			if namespaceNode != nil {
				localName := namespaceNode.Content(content)
				imports[localName] = resolvedPath
			}

			if specifierNode != nil {
				var localName, remoteName string
				nameNode := specifierNode.ChildByFieldName("name")
				aliasNode := specifierNode.ChildByFieldName("alias")

				if nameNode != nil {
					remoteName = nameNode.Content(content)
				}
				if aliasNode != nil {
					localName = aliasNode.Content(content)
				} else {
					localName = remoteName
				}

				if localName != "" && remoteName != "" {
					imports[localName] = fmt.Sprintf("%s:%s", resolvedPath, remoteName)
				}
			}

			if objPatternNode != nil {
				extractJSRequireBindings(objPatternNode, resolvedPath, imports, content)
			}
		}
	}

	resolveTargetID := func(symbol string, currentFile string) string {
		if resolved, ok := imports[symbol]; ok {
			return resolved
		}
		return fmt.Sprintf("%s:%s", currentFile, symbol)
	}

	// 2. Definition Query
	defQueryStr := `
		(function_declaration name: (_) @function.name) @function.def
		(generator_function_declaration name: (_) @function.name) @function.def
		(method_definition name: (_) @method.name) @method.def
		(class_declaration name: (_) @class.name) @class.def

		(field_definition 
			property: (property_identifier) @field.name
		) @field.def

		(variable_declarator 
			name: (identifier) @function.name 
			value: [(arrow_function) (function_expression)]
		) @function.def

		(call_expression
			function: (identifier) @test.wrapper
			arguments: (arguments (string) (arrow_function))
		)
	`
	qDef, err := sitter.NewQuery([]byte(defQueryStr), javascript.GetLanguage())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid definition query: %w", err)
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

		var fieldName string
		var fieldNode *sitter.Node

		for _, c := range m.Captures {
			captureName := qDef.CaptureNameForId(c.Index)
			nodeContent := c.Node.Content(content)

			switch captureName {
			case "field.name":
				fieldName = nodeContent
				fieldNode = c.Node

			case "test.wrapper":
				if nodeContent == "describe" || nodeContent == "it" || nodeContent == "test" {
					argsNode := c.Node.Parent().ChildByFieldName("arguments")
					if argsNode != nil && argsNode.NamedChildCount() > 0 {
						nameNode := argsNode.NamedChild(0)
						if nameNode.Type() == "string" {
							testName := strings.Trim(nameNode.Content(content), "\"'`")

							fqn := fmt.Sprintf("%s:%s", filePath, testName)
							fullID := GenerateNodeID("Function", fqn, "")

							nodes = append(nodes, &graph.Node{
								ID:    fullID,
								Label: "Function",
								Properties: map[string]interface{}{
									"name":       testName,
									"fqn":        fqn,
									"file":       filePath,
									"start_line": int(c.Node.StartPoint().Row + 1),
									"is_test":    true,
								},
							})
						}
					}
				}

			case "class.name", "function.name", "method.name":
				var label string
				var nodeType string

				if strings.HasPrefix(captureName, "class") {
					label = "Class"
					nodeType = "class"
				} else if strings.HasPrefix(captureName, "function") || strings.HasPrefix(captureName, "method") {
					label = "Function"
				}

				searchNode := c.Node
				if nodeType == "class" {
					if p := c.Node.Parent(); p != nil {
						searchNode = p
					}
				}
				enclosingClass := findEnclosingJSClass(searchNode, content)

				var fqn string
				if enclosingClass != "" {
					fqn = fmt.Sprintf("%s:%s.%s", filePath, enclosingClass, nodeContent)
				} else {
					fqn = fmt.Sprintf("%s:%s", filePath, nodeContent)
				}

				fullID := GenerateNodeID(label, fqn, "")

				n := &graph.Node{
					ID:    fullID,
					Label: label,
					Properties: map[string]interface{}{
						"name":       nodeContent,
						"fqn":        fqn,
						"file":       filePath,
						"start_line": int(c.Node.Parent().StartPoint().Row + 1),
						"end_line":   int(c.Node.Parent().EndPoint().Row + 1),
					},
				}
				nodes = append(nodes, n)
			}
		}

		if fieldName != "" && fieldNode != nil {
			parentClass := findEnclosingJSClass(m.Captures[0].Node, content)
			if parentClass != "" {
				classFqn := fmt.Sprintf("%s:%s", filePath, parentClass)
				classID := GenerateNodeID("Class", classFqn, "")

				if classFields[parentClass] == nil {
					classFields[parentClass] = make(map[string]string)
				}
				classFields[parentClass][fieldName] = ""

				fieldFqn := fmt.Sprintf("%s:%s.%s", filePath, parentClass, fieldName)
				fieldID := GenerateNodeID("Field", fieldFqn, "")
				nodes = append(nodes, &graph.Node{
					ID:    fieldID,
					Label: "Field",
					Properties: map[string]interface{}{
						"name":       fieldName,
						"fqn":        fieldFqn,
						"file":       filePath,
						"start_line": int(fieldNode.StartPoint().Row + 1),
					},
				})

				edges = append(edges, &graph.Edge{
					SourceID: classID,
					TargetID: fieldID,
					Type:     "DEFINES",
				})
			}
		}
	}

	// 3. Inheritance Query
	inheritanceQueryStr := `
		(class_declaration
			name: (_) @class.name
			(class_heritage
				(_) @extends.target
			)?
		)
	`
	qInh, err := sitter.NewQuery([]byte(inheritanceQueryStr), javascript.GetLanguage())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid inheritance query: %w", err)
	}
	defer qInh.Close()

	qcInh := sitter.NewQueryCursor()
	defer qcInh.Close()
	qcInh.Exec(qInh, tree.RootNode())

	for {
		m, ok := qcInh.NextMatch()
		if !ok {
			break
		}

		var className string
		var extendsTarget string
		var classNode *sitter.Node

		for _, c := range m.Captures {
			name := qInh.CaptureNameForId(c.Index)
			contentStr := c.Node.Content(content)

			if name == "class.name" {
				className = contentStr
				classNode = c.Node
			} else if name == "extends.target" {
				if contentStr != "extends" {
					extendsTarget = contentStr
				}
			}
		}

		if className != "" {
			searchNode := classNode
			if p := classNode.Parent(); p != nil {
				searchNode = p
			}
			enclosingClass := findEnclosingJSClass(searchNode, content)

			var sourceFqn string
			if enclosingClass != "" {
				sourceFqn = fmt.Sprintf("%s:%s.%s", filePath, enclosingClass, className)
			} else {
				sourceFqn = fmt.Sprintf("%s:%s", filePath, className)
			}
			sourceID := GenerateNodeID("Class", sourceFqn, "")

			if extendsTarget != "" {
				extendsTarget = strings.TrimSpace(extendsTarget)
				targetFqn := resolveTargetID(extendsTarget, filePath)
				edges = append(edges, &graph.Edge{
					SourceID: sourceID,
					TargetID: targetFqn,
					Type:     "EXTENDS",
				})
			}
		}
	}

	// 4. Reference/Call Query
	refQueryStr := `
		(call_expression
		  function: (identifier) @call.target
		) @call.site

		(call_expression
		  function: (member_expression 
            object: (_) @call.object
            property: (property_identifier) @call.target
          )
		) @call.site
		
		(new_expression
		  constructor: (identifier) @call.target
		) @call.site
	`
	qRef, err := sitter.NewQuery([]byte(refQueryStr), javascript.GetLanguage())
	if err != nil {
		return nodes, edges, fmt.Errorf("invalid reference query: %w", err)
	}
	defer qRef.Close()

	qcRef := sitter.NewQueryCursor()
	defer qcRef.Close()
	qcRef.Exec(qRef, tree.RootNode())

	for {
		m, ok := qcRef.NextMatch()
		if !ok {
			break
		}

		var targetName string
		var objectName string
		var callNode *sitter.Node

		for _, c := range m.Captures {
			name := qRef.CaptureNameForId(c.Index)
			if name == "call.target" {
				targetName = c.Node.Content(content)
			}
			if name == "call.object" {
				objectName = c.Node.Content(content)
			}
			if name == "call.site" {
				callNode = c.Node
			}
		}

		if targetName != "" && callNode != nil {
			sourceFuncNode := findEnclosingJSFunctionNode(callNode)
			if sourceFuncNode != nil {
				funcName := extractJSFunctionName(sourceFuncNode, content)
				if funcName != "" {
					enclosingClass := findEnclosingJSClass(sourceFuncNode, content)
					var sourceFqn string
					if enclosingClass != "" {
						sourceFqn = fmt.Sprintf("%s:%s.%s", filePath, enclosingClass, funcName)
					} else {
						sourceFqn = fmt.Sprintf("%s:%s", filePath, funcName)
					}
					sourceID := GenerateNodeID("Function", sourceFqn, "")

					var targetFqn string
					if objectName == "this" {
						if enclosingClass != "" {
							targetFqn = fmt.Sprintf("%s:%s.%s", filePath, enclosingClass, targetName)
						} else {
							targetFqn = resolveTargetID(targetName, filePath)
						}
					} else {
						targetFqn = resolveTargetID(targetName, filePath)
					}

					edges = append(edges, &graph.Edge{
						SourceID: sourceID,
						TargetID: targetFqn,
						Type:     "CALLS",
					})
				}
			}
		}
	}

	return nodes, edges, nil
}

func extractJSRequireBindings(node *sitter.Node, resolvedPath string, imports map[string]string, content []byte) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "shorthand_property_identifier_pattern" {
			localName := child.Content(content)
			imports[localName] = fmt.Sprintf("%s:%s", resolvedPath, localName)
		} else if child.Type() == "pair_pattern" {
			keyNode := child.ChildByFieldName("key")
			valNode := child.ChildByFieldName("value")
			if keyNode != nil && valNode != nil {
				remoteName := keyNode.Content(content)
				localName := valNode.Content(content)
				imports[localName] = fmt.Sprintf("%s:%s", resolvedPath, remoteName)
			}
		}
	}
}

func resolveJSPath(currentFile, importPath string) string {
	importPath = strings.Trim(importPath, "\"'`")
	if strings.HasPrefix(importPath, ".") {
		dir := filepath.Dir(currentFile)
		resolved := filepath.Join(dir, importPath)
		if filepath.Ext(resolved) == "" {
			resolved += ".js"
		}
		return filepath.ToSlash(resolved)
	}
	return importPath
}

func findEnclosingJSClass(n *sitter.Node, content []byte) string {
	curr := n.Parent()
	for curr != nil {
		if curr.Type() == "class_declaration" {
			nameNode := curr.ChildByFieldName("name")
			if nameNode != nil {
				return nameNode.Content(content)
			}
		}
		curr = curr.Parent()
	}
	return ""
}

func findEnclosingJSFunctionNode(n *sitter.Node) *sitter.Node {
	curr := n.Parent()
	for curr != nil {
		t := curr.Type()
		if t == "function_declaration" || t == "generator_function_declaration" || t == "method_definition" {
			return curr
		}
		if t == "arrow_function" || t == "function_expression" {
			if curr.Parent() != nil && curr.Parent().Type() == "variable_declarator" {
				return curr.Parent()
			}
		}
		curr = curr.Parent()
	}
	return nil
}

func extractJSFunctionName(n *sitter.Node, content []byte) string {
	nameNode := n.ChildByFieldName("name")
	if nameNode != nil {
		return nameNode.Content(content)
	}
	return ""
}
