package tools

import (
	"encoding/json"
	"errors"
	"io"
)

func jsonDecoder(reader io.Reader) *json.Decoder {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	return decoder
}

func decodeStrict(reader io.Reader, target any) error {
	decoder := jsonDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
