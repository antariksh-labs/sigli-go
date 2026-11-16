package sigli

import (
	"encoding/base64"
	"os"
	"strings"
)

type FileInput interface {
	resolve() (b64, url string, err error)
}

type fileBytes []byte
type filePath string
type fileURL string

func FileBytes(b []byte) FileInput { return fileBytes(b) }

func FilePath(p string) FileInput { return filePath(p) }

func FileURL(u string) FileInput { return fileURL(u) }

func (f fileBytes) resolve() (string, string, error) {
	return base64.StdEncoding.EncodeToString(f), "", nil
}

func (f filePath) resolve() (string, string, error) {
	b, err := os.ReadFile(string(f))
	if err != nil {
		return "", "", argError("cannot read file: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b), "", nil
}

func (f fileURL) resolve() (string, string, error) {
	u := string(f)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", argError("file URL must start with http:// or https://")
	}
	return "", u, nil
}

func applyFile(body map[string]any, f FileInput) error {
	if f == nil {
		return argError("a file input is required")
	}
	b64, u, err := f.resolve()
	if err != nil {
		return err
	}
	if u != "" {
		body["file_url"] = u
	} else {
		body["file_base64"] = b64
	}
	return nil
}

func withParams(params map[string]any) (map[string]any, error) {
	body := make(map[string]any, len(params)+1)
	for k, v := range params {
		body[k] = v
	}
	if f, ok := body["file"]; ok {
		fi, isFile := f.(FileInput)
		if !isFile {
			return nil, argError(`"file" must be a sigli.FileInput (FileBytes/FilePath/FileURL)`)
		}
		delete(body, "file")
		if err := applyFile(body, fi); err != nil {
			return nil, err
		}
	}
	return body, nil
}
