package analysis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGolangParser_Registration(t *testing.T) {
	parser, ok := GetParser(".go")
	assert.True(t, ok, "Go parser should be registered for .go extension")
	assert.NotNil(t, parser, "Go parser should not be nil")
}

func TestGolangParser_ParseDefinitions(t *testing.T) {
	parser, ok := GetParser(".go")
	assert.True(t, ok)

	content := []byte(`
package main

import "fmt"

type Service interface {
	DoWork()
}

type User struct {
	ID   int
	Name string
}

func (u *User) Save() {
	fmt.Println("Saving user")
}

func CreateUser() *User {
	return &User{}
}
`)

	nodes, edges, err := parser.Parse("main.go", content)
	assert.NoError(t, err)

	// Validate nodes
	var foundInterface, foundStruct, foundFieldID, foundFieldName, foundMethod, foundFunc bool

	for _, n := range nodes {
		name := n.Properties["name"].(string)
		fqn := n.Properties["fqn"].(string)

		switch n.Label {
		case "Interface":
			if name == "Service" {
				foundInterface = true
				assert.Equal(t, "main.go:main.Service", fqn)
				assert.Equal(t, 6, n.Properties["start_line"])
			}
		case "Class":
			if name == "User" {
				foundStruct = true
				assert.Equal(t, "main.go:main.User", fqn)
				assert.Equal(t, 10, n.Properties["start_line"])
			}
		case "Field":
			if name == "ID" {
				foundFieldID = true
				assert.Equal(t, "main.go:main.User.ID", fqn)
			} else if name == "Name" {
				foundFieldName = true
				assert.Equal(t, "main.go:main.User.Name", fqn)
			}
		case "Function":
			if name == "Save" {
				foundMethod = true
				assert.Equal(t, "main.go:main.User.Save", fqn)
				assert.Equal(t, 15, n.Properties["start_line"])
			} else if name == "CreateUser" {
				foundFunc = true
				assert.Equal(t, "main.go:main.CreateUser", fqn)
				assert.Equal(t, 19, n.Properties["start_line"])
			}
		}
	}

	assert.True(t, foundInterface, "Service interface not found")
	assert.True(t, foundStruct, "User struct not found")
	assert.True(t, foundFieldID, "ID field not found")
	assert.True(t, foundFieldName, "Name field not found")
	assert.True(t, foundMethod, "Save method not found")
	assert.True(t, foundFunc, "CreateUser function not found")

	// Validate DEFINES edges
	var foundDefID, foundDefName, foundDefMethod bool
	for _, e := range edges {
		if e.Type == "DEFINES" {
			if strings.Contains(e.SourceID, "User") && strings.Contains(e.TargetID, "ID") {
				foundDefID = true
			}
			if strings.Contains(e.SourceID, "User") && strings.Contains(e.TargetID, "Name") {
				foundDefName = true
			}
			if strings.Contains(e.SourceID, "User") && strings.Contains(e.TargetID, "Save") {
				foundDefMethod = true
			}
		}
	}

	assert.True(t, foundDefID, "DEFINES edge User -> ID not found")
	assert.True(t, foundDefName, "DEFINES edge User -> Name not found")
	assert.True(t, foundDefMethod, "DEFINES edge User -> Save not found")
}

func TestGolangParser_ParseEmbedding(t *testing.T) {
	parser, ok := GetParser(".go")
	assert.True(t, ok)

	content := []byte(`
package model

type Person struct {
	Age int
}

type Employee struct {
	Person
	Salary float64
}

type Reader interface {
	Read()
}

type ReadWriter interface {
	Reader
	Write()
}
`)

	_, edges, err := parser.Parse("model.go", content)
	assert.NoError(t, err)

	var foundStructEmbedding, foundInterfaceEmbedding bool
	for _, e := range edges {
		if e.Type == "EXTENDS" {
			if strings.Contains(e.SourceID, "model.go:model.Employee") && strings.Contains(e.TargetID, "model.go:model.Person") {
				foundStructEmbedding = true
			}
			if strings.Contains(e.SourceID, "model.go:model.ReadWriter") && strings.Contains(e.TargetID, "model.go:model.Reader") {
				foundInterfaceEmbedding = true
			}
		}
	}

	assert.True(t, foundStructEmbedding, "EXTENDS edge Employee -> Person (struct embedding) not found")
	assert.True(t, foundInterfaceEmbedding, "EXTENDS edge ReadWriter -> Reader (interface embedding) not found")
}

func TestGolangParser_ParseCalls(t *testing.T) {
	parser, ok := GetParser(".go")
	assert.True(t, ok)

	content := []byte(`
package controller

import (
	"fmt"
	"graphdb/internal/graph"
)

type Controller struct {
	db *graph.Node
}

func (c *Controller) Handle() {
	c.Process()
	fmt.Println("Handled")
	helperFunc()
}

func (c *Controller) Process() {}

func helperFunc() {}
`)

	_, edges, err := parser.Parse("controller.go", content)
	assert.NoError(t, err)

	var foundReceiverCall, foundStdLibCall, foundLocalCall bool
	for _, e := range edges {
		if e.Type == "CALLS" {
			if strings.Contains(e.SourceID, "Controller.Handle") {
				if strings.Contains(e.TargetID, "Controller.Process") {
					foundReceiverCall = true
				}
				if strings.Contains(e.TargetID, "fmt.Println") {
					foundStdLibCall = true
				}
				if strings.Contains(e.TargetID, "controller.go:controller.helperFunc") {
					foundLocalCall = true
				}
			}
		}
	}

	assert.True(t, foundReceiverCall, "CALLS edge Handle -> Process (receiver call) not found")
	assert.True(t, foundStdLibCall, "CALLS edge Handle -> fmt.Println (standard library call) not found")
	assert.True(t, foundLocalCall, "CALLS edge Handle -> helperFunc (local call) not found")
}
