package pos

import (
	"encoding/json"
	"strconv"
)

// ToJSON encodes a value to string
func ToJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// FormatPrice formats price with 2 decimal places
func FormatPrice(price float64) string {
	return strconv.FormatFloat(price, 'f', 2, 64)
}

