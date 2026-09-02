package unifiedpay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const maximumJSONDepth = 100

func strictUnmarshalObject(body []byte, destination any, disallowUnknownFields bool) error {
	if err := validateStrictJSON(body); err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimLeft(string(body), " \r\n\t"), "{") {
		return ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidJSON
	}
	return nil
}

func validateStrictJSON(body []byte) error {
	if len(body) == 0 || !utf8.Valid(body) {
		return ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, 0); err != nil {
		if errors.Is(err, ErrDuplicateJSONKey) {
			return ErrDuplicateJSONKey
		}
		return ErrInvalidJSON
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrTrailingJSON
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maximumJSONDepth {
		return ErrInvalidJSON
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			keys := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return ErrInvalidJSON
				}
				if _, exists := keys[key]; exists {
					return ErrDuplicateJSONKey
				}
				keys[key] = struct{}{}
				if err := consumeJSONValue(decoder, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return ErrInvalidJSON
			}
			return nil
		case '[':
			for decoder.More() {
				if err := consumeJSONValue(decoder, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalidJSON
			}
			return nil
		default:
			return ErrInvalidJSON
		}
	case string, json.Number, bool, nil:
		return nil
	default:
		return ErrInvalidJSON
	}
}
