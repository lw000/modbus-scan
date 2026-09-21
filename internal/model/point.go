package model

import "time"

// Point is a persisted point configuration owned by one device.
type Point struct {
	ID          int64     `json:"id"`
	DeviceID    int64     `json:"device_id"`
	TagName     string    `json:"tag_name"`
	Description string    `json:"description"`
	RegType     string    `json:"reg_type"`
	Address     uint16    `json:"address"`
	DataType    string    `json:"data_type"`
	BitOffset   int       `json:"bit_offset"`
	BitLen      int       `json:"bit_len"`
	Writeable   int       `json:"writeable"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FieldError identifies an invalid field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// RowError identifies an invalid CSV field by row.
type RowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}
