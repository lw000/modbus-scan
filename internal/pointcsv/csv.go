// Package pointcsv implements strict point CSV import and export.
package pointcsv

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"modbus-scan/internal/config"
	"modbus-scan/internal/model"
)

var canonicalHeader = []string{"TagName", "RegType", "Address", "DataType", "BitOffset", "BitLen", "Writeable", "Description"}

// Parse reads and strictly validates a point CSV document.
func Parse(r io.Reader) ([]model.Point, []model.RowError, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV header: %w", err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\uFEFF")
	}
	if !equalRecord(header, canonicalHeader) {
		return nil, nil, fmt.Errorf("CSV header must be %s", strings.Join(canonicalHeader, ","))
	}

	points := make([]model.Point, 0)
	rowErrors := make([]model.RowError, 0)
	seen := make(map[string]struct{})
	for row := 2; ; row++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read CSV row %d: %w", row, err)
		}
		if len(record) != len(canonicalHeader) {
			rowErrors = append(rowErrors, model.RowError{Row: row, Field: "row", Message: "must contain exactly 8 columns"})
			continue
		}
		point, fieldErrors := parseRecord(record)
		if _, exists := seen[point.TagName]; point.TagName != "" && exists {
			fieldErrors = append(fieldErrors, model.FieldError{Field: "tag_name", Message: "duplicate TagName"})
		}
		if len(fieldErrors) == 0 {
			seen[point.TagName] = struct{}{}
			points = append(points, point)
			continue
		}
		for _, fieldErr := range fieldErrors {
			rowErrors = append(rowErrors, model.RowError{Row: row, Field: fieldErr.Field, Message: fieldErr.Message})
		}
	}
	if len(rowErrors) > 0 {
		return nil, rowErrors, nil
	}
	if len(points) == 0 {
		return nil, nil, fmt.Errorf("CSV must contain at least one point")
	}
	return points, nil, nil
}

func parseRecord(record []string) (model.Point, []model.FieldError) {
	point := model.Point{
		TagName:     strings.TrimSpace(record[0]),
		RegType:     strings.TrimSpace(record[1]),
		DataType:    strings.TrimSpace(record[3]),
		Description: strings.TrimSpace(record[7]),
	}
	errors := make([]model.FieldError, 0)
	address, err := strconv.ParseUint(strings.TrimSpace(record[2]), 10, 16)
	if err != nil {
		errors = append(errors, model.FieldError{Field: "address", Message: "must be an integer between 0 and 65535"})
	} else {
		point.Address = uint16(address)
	}
	bitOffset, err := strconv.Atoi(strings.TrimSpace(record[4]))
	if err != nil {
		errors = append(errors, model.FieldError{Field: "bit_offset", Message: "must be an integer"})
	} else {
		point.BitOffset = bitOffset
	}
	bitLen, err := strconv.Atoi(strings.TrimSpace(record[5]))
	if err != nil {
		errors = append(errors, model.FieldError{Field: "bit_len", Message: "must be an integer"})
	} else {
		point.BitLen = bitLen
	}
	writeable, err := strconv.Atoi(strings.TrimSpace(record[6]))
	if err != nil || (writeable != 0 && writeable != 1) {
		errors = append(errors, model.FieldError{Field: "writeable", Message: "must be 0 or 1"})
	} else {
		point.Writeable = writeable
	}
	if len(errors) == 0 {
		errors = append(errors, config.ValidatePoint(point)...)
	}
	return point, errors
}

// Write writes canonical, stably ordered point CSV.
func Write(w io.Writer, points []model.Point) error {
	ordered := append([]model.Point(nil), points...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].RegType != ordered[j].RegType {
			return ordered[i].RegType < ordered[j].RegType
		}
		if ordered[i].Address != ordered[j].Address {
			return ordered[i].Address < ordered[j].Address
		}
		return ordered[i].TagName < ordered[j].TagName
	})
	writer := csv.NewWriter(w)
	if err := writer.Write(canonicalHeader); err != nil {
		return fmt.Errorf("write CSV header: %w", err)
	}
	for _, point := range ordered {
		record := []string{point.TagName, point.RegType, strconv.FormatUint(uint64(point.Address), 10), point.DataType, strconv.Itoa(point.BitOffset), strconv.Itoa(point.BitLen), strconv.Itoa(point.Writeable), point.Description}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("write CSV point: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush CSV: %w", err)
	}
	return nil
}

func equalRecord(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if strings.TrimSpace(left[i]) != right[i] {
			return false
		}
	}
	return true
}
