package proc

import (
	"io"
	"os"
)

func Redirector() (reader io.ReadCloser, output OutputRedirect, err error) {
	reader, output.File, err = os.Pipe()

	return reader, output, err
}
