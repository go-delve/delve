package dap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// min returns the lowest-valued integer
// between the two passed into it.
func min(i, j int) int {
	if i < j {
		return i
	}
	return j
}

// mapToStruct converts map[string]interface{} to the struct type object.
// output must be a pointer to the struct object.
func mapToStruct(input map[string]interface{}, output interface{}) error {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(input); err != nil {
		return err
	}
	if err := json.NewDecoder(buf).Decode(output); err != nil && err != io.EOF {
		if uerr, ok := err.(*json.UnmarshalTypeError); ok {
			// Format json.UnmarshalTypeError error string in our own way. E.g.,
			//   "json: cannot unmarshal number into Go struct field LaunchArgs.program of type string" (go1.16)
			//   => "cannot unmarshal number into 'program' of type string"
			return fmt.Errorf("cannot unmarshal %v into %q of type %v", uerr.Value, uerr.Field, uerr.Type.String())
		}
		return err
	}
	return nil
}
