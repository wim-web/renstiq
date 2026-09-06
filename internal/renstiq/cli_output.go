package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Result is the init envelope; read commands have their own wire types.
type Result struct {
	Version int         `json:"version"`
	Command string      `json:"command"`
	Init    *InitResult `json:"init,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func emitJSON(out, log io.Writer, result any, err error) int {
	code := 0
	if err != nil {
		code = 1
		var input *InputError
		if errors.As(err, &input) {
			code = 2
		}
		fmt.Fprintln(log, err)
	}
	payload := asMap(result)
	payload["version"] = 1
	if err != nil {
		payload["error"] = err.Error()
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if e := enc.Encode(payload); e != nil {
		fmt.Fprintln(log, e)
		return 1
	}
	return code
}
func jsonAction[T any](run func(context.Context, io.Reader) (T, error)) cliAction {
	return func(ctx context.Context, in io.Reader, out, log io.Writer) int {
		result, err := run(ctx, in)
		return emitJSON(out, log, result, err)
	}
}
func writeText(out, log io.Writer, text string) int {
	if _, err := io.WriteString(out, text); err != nil {
		fmt.Fprintln(log, err)
		return 1
	}
	return 0
}
