/*
Copyright 2019 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"

	"vitess.io/vitess/go/exit"
	"vitess.io/vitess/go/vt/log"

	"vitess.io/vitess/go/vt/sqlparser/visitorgen"
)

var (
	inputFile  = flag.String("input", "", "input file to use")
	outputFile = flag.String("output", "", "output file")
	compare    = flag.Bool("compareOnly", false, "instead of writing to the output file, compare if the generated visitor is still valid for this ast.go")
)

const usage = `Usage of visitorgen:

go run /path/to/visitorgen/main -input=/path/to/ast.go -output=/path/to/rewriter.go
`

func main() {
	defer exit.Recover()
	flag.Usage = printUsage
	flag.Parse()

	if *inputFile == "" || *outputFile == "" {
		printUsage()
		exit.Return(1)
	}

	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, *inputFile, nil, parser.DeclarationErrors)
	if err != nil {
		log.Error(err)
		exit.Return(1)
	}

	astWalkResult := visitorgen.Walk(file)
	vp := visitorgen.Transform(astWalkResult)
	vd := visitorgen.ToVisitorPlan(vp)

	replacementMethods := visitorgen.EmitReplacementMethods(vd)
	typeSwitch := visitorgen.EmitTypeSwitches(vd)

	b := &bytes.Buffer{}
	fmt.Fprint(b, fileHeader)
	fmt.Fprintln(b)
	fmt.Fprintln(b, replacementMethods)
	fmt.Fprint(b, applyHeader)
	fmt.Fprintln(b, typeSwitch)
	fmt.Fprintln(b, fileFooter)

	if *compare {
		currentFile, err := ioutil.ReadFile(*outputFile)
		if err != nil {
			log.Error(err)
			exit.Return(1)
		}
		if !bytes.Equal(b.Bytes(), currentFile) {
			fmt.Println("rewriter needs to be re-generated: go generate " + *outputFile)
			exit.Return(1)
		}
	} else {
		err = ioutil.WriteFile(*outputFile, b.Bytes(), 0644)
		if err != nil {
			log.Error(err)
			exit.Return(1)
		}
	}

}

func printUsage() {
	os.Stderr.WriteString(usage)
	os.Stderr.WriteString("\nOptions:\n")
	flag.PrintDefaults()
}

const fileHeader = `// Code generated by visitorgen/main/main.go. DO NOT EDIT.

package sqlparser

//go:generate go run ./visitorgen/main -input=ast.go -output=rewriter.go

import (
	"reflect"
)

type replacerFunc func(newNode, parent SQLNode)

// application carries all the shared data so we can pass it around cheaply.
type application struct {
	pre, post ApplyFunc
	cursor    Cursor
}
`

const applyHeader = `
// apply is where the visiting happens. Here is where we keep the big switch-case that will be used
// to do the actual visiting of SQLNodes
func (a *application) apply(parent, node SQLNode, replacer replacerFunc) {
	if node == nil || isNilValue(node) {
		return
	}

	// avoid heap-allocating a new cursor for each apply call; reuse a.cursor instead
	saved := a.cursor
	a.cursor.replacer = replacer
	a.cursor.node = node
	a.cursor.parent = parent

	if a.pre != nil && !a.pre(&a.cursor) {
		a.cursor = saved
		return
	}

	// walk children
	// (the order of the cases is alphabetical)
	switch n := node.(type) {
	case nil:
	`

const fileFooter = `
	default:
		panic("unknown ast type " + reflect.TypeOf(node).String())
	}

	if a.post != nil && !a.post(&a.cursor) {
		panic(abort)
	}

	a.cursor = saved
}

func isNilValue(i interface{}) bool {
	valueOf := reflect.ValueOf(i)
	kind := valueOf.Kind()
	isNullable := kind == reflect.Ptr || kind == reflect.Array || kind == reflect.Slice
	return isNullable && valueOf.IsNil()
}`