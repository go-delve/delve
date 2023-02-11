package proc

import "os"

type Mode string

const (
	RedirectFileMode Mode = "file"
	RedirectPathMode Mode = "path"
)

type Redirect struct {
	WriterFiles [3]*os.File
	ReaderFiles [3]*os.File
	Paths       [3]string
	Mode        Mode
}

func NewEmptyRedirectByPath() Redirect {
	return Redirect{Mode: RedirectPathMode}
}

func NewRedirectByPath(paths [3]string) Redirect {
	return Redirect{Paths: paths, Mode: RedirectPathMode}
}

func NewRedirectByFile(readerFiles [3]*os.File, writerFiles [3]*os.File) Redirect {
	return Redirect{ReaderFiles: readerFiles, WriterFiles: writerFiles, Mode: RedirectFileMode}
}
