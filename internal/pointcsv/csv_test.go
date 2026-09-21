package pointcsv

import (
	"bytes"
	"strings"
	"testing"
)

const header = "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n"

func TestParseValidPoints(t *testing.T) {
	input := header +
		"Mode,HoldingReg,10,UInt16,4,2,0,模式\n" +
		"Running,CoilStatus,1,Bool,0,0,1,运行状态\n"
	points, rowErrors, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(rowErrors) != 0 || len(points) != 2 {
		t.Fatalf("points = %#v, row errors = %#v", points, rowErrors)
	}
	if points[0].BitOffset != 4 || points[0].BitLen != 2 || points[1].Writeable != 1 || points[1].Description != "运行状态" {
		t.Fatalf("points = %#v", points)
	}
}

func TestParseRejectsBadHeader(t *testing.T) {
	_, _, err := Parse(strings.NewReader("Name,Type\nA,B\n"))
	if err == nil {
		t.Fatal("expected header error")
	}
}

func TestParseCollectsStrictRowErrors(t *testing.T) {
	input := header +
		"Tail,HoldingReg,65535,Double,0,0,0,\n" +
		"Flag,HoldingReg,1,UInt16,0,1,maybe,\n" +
		"Flag,HoldingReg,2,UInt16,0,1,0,\n"
	points, rowErrors, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if points != nil {
		t.Fatalf("invalid import returned points: %#v", points)
	}
	if len(rowErrors) != 2 {
		t.Fatalf("row errors = %#v", rowErrors)
	}
	if rowErrors[0].Row != 2 || rowErrors[0].Field != "address" {
		t.Fatalf("first error = %#v", rowErrors[0])
	}
	if rowErrors[1].Row != 3 || rowErrors[1].Field != "writeable" {
		t.Fatalf("second error = %#v", rowErrors[1])
	}
}

func TestParseRejectsWrongColumnCount(t *testing.T) {
	_, rowErrors, err := Parse(strings.NewReader(header + "A,HoldingReg,0,UInt16,0,16,0,description,extra\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rowErrors) != 1 || rowErrors[0].Field != "row" {
		t.Fatalf("row errors = %#v", rowErrors)
	}
}

func TestWriteUsesCanonicalOrder(t *testing.T) {
	points, rowErrors, err := Parse(strings.NewReader(header +
		"B,InputReg,2,UInt16,0,16,0,B\n" +
		"A,HoldingReg,3,UInt16,0,16,0,A\n" +
		"C,HoldingReg,1,UInt16,0,16,1,C\n"))
	if err != nil || len(rowErrors) != 0 {
		t.Fatalf("parse errors = %v %#v", err, rowErrors)
	}
	var output bytes.Buffer
	if err := Write(&output, points); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "C,") || !strings.HasPrefix(lines[2], "A,") {
		t.Fatalf("output = %q", output.String())
	}
	if !strings.Contains(output.String(), ",1,C") {
		t.Fatalf("output does not contain numeric writeable and description: %q", output.String())
	}
}

func TestParseRejectsBooleanWriteable(t *testing.T) {
	_, rowErrors, err := Parse(strings.NewReader(header + "A,HoldingReg,0,UInt16,0,16,true,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rowErrors) != 1 || rowErrors[0].Field != "writeable" {
		t.Fatalf("row errors = %#v", rowErrors)
	}
}

func TestParseRejectsLongDescription(t *testing.T) {
	input := header + "A,HoldingReg,0,UInt16,0,16,0," + strings.Repeat("测", 256) + "\n"
	_, rowErrors, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(rowErrors) != 1 || rowErrors[0].Field != "description" {
		t.Fatalf("row errors = %#v", rowErrors)
	}
}
