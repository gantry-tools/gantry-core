// Package protocol contains bounded wire and presentation primitives shared by
// Gantry HTTP and CLI adapters.
package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/gantry-tools/gantry-core/cli"
)

const DefaultMaxBody int64 = 1 << 20

type ErrorCode string

const (
	Invalid      ErrorCode = "invalid"
	Unauthorized ErrorCode = "unauthorized"
	Forbidden    ErrorCode = "forbidden"
	NotFound     ErrorCode = "not_found"
	Conflict     ErrorCode = "conflict"
	Unavailable  ErrorCode = "unavailable"
	Internal     ErrorCode = "internal"
	Partial      ErrorCode = "partial"
)

type Error struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func (e *Error) HTTPStatus() int {
	switch e.Code {
	case Invalid:
		return http.StatusBadRequest
	case Unauthorized:
		return http.StatusUnauthorized
	case Forbidden:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case Conflict:
		return http.StatusConflict
	case Unavailable:
		return http.StatusServiceUnavailable
	case Partial:
		return http.StatusMultiStatus
	default:
		return http.StatusInternalServerError
	}
}

func ExitCode(err error) int {
	if err == nil {
		return cli.ExitOK
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		return cli.ExitFailure
	}
	switch apiErr.Code {
	case Invalid:
		return cli.ExitUsage
	case Unauthorized, Forbidden:
		return cli.ExitAuth
	case NotFound:
		return cli.ExitNotFound
	case Conflict:
		return cli.ExitConflict
	case Unavailable:
		return cli.ExitUnavailable
	case Partial:
		return cli.ExitPartial
	default:
		return cli.ExitFailure
	}
}

func DecodeStrict(reader io.Reader, maximum int64, target any) error {
	if maximum <= 0 {
		maximum = DefaultMaxBody
	}
	limited := io.LimitReader(reader, maximum+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(body)) > maximum {
		return &Error{Code: Invalid, Message: "JSON input exceeds the size limit"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &Error{Code: Invalid, Message: "invalid JSON input", Details: map[string]any{"reason": err.Error()}}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &Error{Code: Invalid, Message: "JSON input must contain exactly one value"}
	}
	return nil
}

type Page struct {
	Number int `json:"number"`
	Size   int `json:"size"`
}

func NormalizePage(number, size, maximum int) Page {
	if number < 1 {
		number = 1
	}
	if maximum < 1 {
		maximum = 100
	}
	if size < 1 {
		size = 50
	}
	if size > maximum {
		size = maximum
	}
	return Page{Number: number, Size: size}
}

func Confirm(expected, actual string) error {
	if expected == "" || actual != expected {
		return &Error{Code: Invalid, Message: "confirmation text did not match"}
	}
	return nil
}

func NewRequestID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func ValidKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r) {
			continue
		}
		return false
	}
	return true
}

// Redact returns a JSON-compatible deep copy with the declared JSON pointers
// replaced. Missing pointers are ignored so response versions can evolve.
func Redact(value any, pointers []string) (any, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var clone any
	if err := json.Unmarshal(body, &clone); err != nil {
		return nil, err
	}
	for _, pointer := range pointers {
		parts, err := pointerParts(pointer)
		if err != nil {
			return nil, err
		}
		redactAt(clone, parts)
	}
	return clone, nil
}

func pointerParts(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if pointer[0] != '/' {
		return nil, fmt.Errorf("invalid JSON pointer %q", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func redactAt(current any, parts []string) {
	if len(parts) == 0 {
		return
	}
	if object, ok := current.(map[string]any); ok {
		if len(parts) == 1 {
			if _, exists := object[parts[0]]; exists {
				object[parts[0]] = "[redacted]"
			}
			return
		}
		redactAt(object[parts[0]], parts[1:])
		return
	}
	if array, ok := current.([]any); ok {
		index, err := strconv.Atoi(parts[0])
		if err == nil && index >= 0 && index < len(array) {
			if len(parts) == 1 {
				array[index] = "[redacted]"
			} else {
				redactAt(array[index], parts[1:])
			}
		}
	}
}

type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

func Render(writer io.Writer, mode cli.Output, value any) error {
	if mode == cli.JSON {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	}
	table, ok := value.(Table)
	if !ok {
		if text, textOK := value.(string); textOK {
			_, err := fmt.Fprintln(writer, text)
			return err
		}
		return errors.New("plain and table output require protocol.Table or string")
	}
	if mode == cli.Plain {
		for _, row := range table.Rows {
			if _, err := fmt.Fprintln(writer, strings.Join(row, "\t")); err != nil {
				return err
			}
		}
		return nil
	}
	tabs := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if len(table.Columns) > 0 {
		fmt.Fprintln(tabs, strings.Join(table.Columns, "\t"))
	}
	for _, row := range table.Rows {
		fmt.Fprintln(tabs, strings.Join(row, "\t"))
	}
	return tabs.Flush()
}
