package proc

import "os"

type OutputRedirect struct {
	Path string
	File *os.File
}

func NewEmptyRedirect() [3]OutputRedirect {
	return [3]OutputRedirect{}
}

func NewRedirectByPath(paths [3]string) [3]OutputRedirect {
	return [3]OutputRedirect{{Path: paths[0]}, {Path: paths[1]}, {Path: paths[2]}}
}

func NewRedirectByFile(writerFiles [3]*os.File) [3]OutputRedirect {
	return [3]OutputRedirect{{File: writerFiles[0]}, {File: writerFiles[1]}, {File: writerFiles[2]}}
}
