package goadesignupgrader_test

import (
	"testing"

	"github.com/goadesign/goadesignupgrader"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, goadesignupgrader.Analyzer, "design")
}
