package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modbus-scan/internal/model"
)

func TestValidatePointRejectsInvalidWriteable(t *testing.T) {
	point := model.Point{TagName: "A", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16, Writeable: 2}
	assertPointFieldError(t, ValidatePoint(point), "writeable")
}

func TestValidatePointRejectsLongDescription(t *testing.T) {
	point := model.Point{TagName: "A", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16, Description: strings.Repeat("测", 256)}
	assertPointFieldError(t, ValidatePoint(point), "description")
}

func assertPointFieldError(t *testing.T, errors []model.FieldError, field string) {
	t.Helper()
	for _, fieldError := range errors {
		if fieldError.Field == field {
			return
		}
	}
	t.Fatalf("errors = %#v, want field %q", errors, field)
}

func TestTypeBitWidth(t *testing.T) {
	tests := []struct {
		dataType string
		want     int
	}{
		{"Bool", 1},
		{"Int16", 16},
		{"UInt16", 16},
		{"Int32", 32},
		{"UInt32", 32},
		{"Float32", 32},
		{"Double", 64},
		{"Unknown", 16}, // default fallback
	}
	for _, tt := range tests {
		t.Run(tt.dataType, func(t *testing.T) {
			got := TypeBitWidth(tt.dataType)
			if got != tt.want {
				t.Errorf("TypeBitWidth(%q) = %d, want %d", tt.dataType, got, tt.want)
			}
		})
	}
}

func TestIsRegTypeBit(t *testing.T) {
	tests := []struct {
		regType string
		want    bool
	}{
		{"CoilStatus", true},
		{"InputStatus", true},
		{"HoldingReg", false},
		{"InputReg", false},
		{"Unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.regType, func(t *testing.T) {
			if got := IsRegTypeBit(tt.regType); got != tt.want {
				t.Errorf("IsRegTypeBit(%q) = %v, want %v", tt.regType, got, tt.want)
			}
		})
	}
}

func TestPointConfig_GetRegisterCount(t *testing.T) {
	tests := []struct {
		name     string
		regType  string
		dataType string
		want     uint16
	}{
		{"CoilStatus Bool", "CoilStatus", "Bool", 1},
		{"InputStatus Bool", "InputStatus", "Bool", 1},
		{"HoldingReg Int16", "HoldingReg", "Int16", 1},
		{"HoldingReg UInt16", "HoldingReg", "UInt16", 1},
		{"HoldingReg Bool", "HoldingReg", "Bool", 1},
		{"HoldingReg Int32", "HoldingReg", "Int32", 2},
		{"HoldingReg Float32", "HoldingReg", "Float32", 2},
		{"HoldingReg Double", "HoldingReg", "Double", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PointConfig{RegType: tt.regType, DataType: tt.dataType}
			if got := p.GetRegisterCount(); got != tt.want {
				t.Errorf("GetRegisterCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLoadPointsFromCSV_Valid(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"Temperature,HoldingReg,0,Int16,0,16,0,temperature\n" +
		"Pressure,HoldingReg,1,Int32,0,32,1,\n" +
		"RunStatus,CoilStatus,0,Bool,0,0,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "points.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	points, err := LoadPointsFromCSV(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	if points[0].TagName != "Temperature" {
		t.Errorf("TagName = %q", points[0].TagName)
	}
	if !points[1].Writeable {
		t.Error("Writeable = false, want true")
	}
	if points[0].Description != "temperature" {
		t.Errorf("Description = %q", points[0].Description)
	}
	if points[2].RegType != "CoilStatus" {
		t.Errorf("RegType = %q", points[2].RegType)
	}
}

func TestLoadPointsFromCSV_DuplicateTagName(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,HoldingReg,0,Int16,0,16,0,\n" +
		"A,HoldingReg,1,Int16,0,16,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "dupes.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	points, err := LoadPointsFromCSV(path)
	if err == nil || points != nil {
		t.Fatalf("points = %#v, err = %v", points, err)
	}
}

func TestLoadPointsFromCSV_InvalidTagNameSkipped(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"Good_1,HoldingReg,0,Int16,0,16,0,\n" +
		"1Bad,HoldingReg,1,Int16,0,16,0,\n" +
		"Bad-Name,HoldingReg,2,Int16,0,16,0,\n" +
		"Bad Name,HoldingReg,3,Int16,0,16,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "invalid_tag.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	points, err := LoadPointsFromCSV(path)
	if err == nil || points != nil {
		t.Fatalf("points = %#v, err = %v", points, err)
	}
}

func TestLoadPointsFromCSV_RejectsPartialValidity(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"Good,HoldingReg,0,UInt16,0,16,0,\n" +
		"Bad,HoldingReg,1,UInt16,0,0,0,\n"
	path := filepath.Join(t.TempDir(), "strict.csv")
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	if points, err := LoadPointsFromCSV(path); err == nil || points != nil {
		t.Fatalf("points = %#v, err = %v", points, err)
	}
}

func TestLoadPointsFromCSV_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")
	if err := os.WriteFile(path, []byte("TagName\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error for empty CSV")
	}
}

func TestLoadPointsFromCSV_NoValidPoints(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"Bad1,BadRegType,0,Int16,0,16,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error when no valid points")
	}
}

func TestLoadPointsFromCSV_InvalidDataType(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,HoldingReg,0,Int64,0,16,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "bad_type.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error for invalid data type")
	}
}

func TestLoadPointsFromCSV_CoilStatusNonBool(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,CoilStatus,0,Int16,0,0,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "coil_nonbool.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error: coil type must be Bool")
	}
}

func TestLoadPointsFromCSV_BitOffsetZeroBitLenZeroNonBool(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,HoldingReg,0,Int16,0,0,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "bit_offset_zero.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error: BitOffset=0 BitLen=0 invalid for non-Bool")
	}
}

func TestOptimizeChunks_Simple(t *testing.T) {
	points := []PointConfig{
		{TagName: "A", RegType: "HoldingReg", Address: 0, DataType: "Int16", BitLen: 16},
		{TagName: "B", RegType: "HoldingReg", Address: 1, DataType: "Int16", BitLen: 16},
		{TagName: "C", RegType: "CoilStatus", Address: 0, DataType: "Bool"},
	}

	chunks := OptimizeChunks(points)

	holdingChunks, ok := chunks["HoldingReg"]
	if !ok {
		t.Fatal("expected HoldingReg chunks")
	}
	if len(holdingChunks) != 1 {
		t.Fatalf("expected 1 HoldingReg chunk, got %d", len(holdingChunks))
	}
	if holdingChunks[0].Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", holdingChunks[0].Quantity)
	}
	if len(holdingChunks[0].Points) != 2 {
		t.Errorf("Points count = %d, want 2", len(holdingChunks[0].Points))
	}
}

func TestOptimizeChunks_SplitByGap(t *testing.T) {
	points := []PointConfig{
		{TagName: "A", RegType: "HoldingReg", Address: 0, DataType: "Int16", BitLen: 16},
		{TagName: "B", RegType: "HoldingReg", Address: 10, DataType: "Int16", BitLen: 16},
	}

	chunks := OptimizeChunks(points)
	holdingChunks := chunks["HoldingReg"]
	if len(holdingChunks) != 2 {
		t.Fatalf("expected 2 chunks (gapped), got %d", len(holdingChunks))
	}
}

func TestOptimizeChunks_ByRegType(t *testing.T) {
	points := []PointConfig{
		{TagName: "A", RegType: "HoldingReg", Address: 0, DataType: "Int16", BitLen: 16},
		{TagName: "B", RegType: "InputReg", Address: 0, DataType: "Int16", BitLen: 16},
	}

	chunks := OptimizeChunks(points)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 reg type groups, got %d", len(chunks))
	}
}

func TestLoadPointsFromCSV_BoolInRegister(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,HoldingReg,0,Bool,0,0,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "bool_reg.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	points, err := LoadPointsFromCSV(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
}

func TestLoadPointsFromCSV_BoolWithBitOffset(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,HoldingReg,0,Bool,3,1,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "bool_bit.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error: Bool in register cannot have BitOffset/BitLen")
	}
}

func TestLoadPointsFromCSV_FloatWithBitExtract(t *testing.T) {
	csv := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"A,HoldingReg,0,Float32,1,1,0,\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "float_bit.csv")
	if err := os.WriteFile(path, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPointsFromCSV(path)
	if err == nil {
		t.Fatal("expected error: Float32 cannot have bit extract")
	}
}
