package api

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"text/tabwriter"
)

const (
	// strings longer than this will cause slices, arrays and structs to be printed on multiple lines when newlines is enabled
	maxShortStringLen = 7
	// string used for one indentation level (when printing on multiple lines)
	indentString = "\t"
)

// SinglelineString returns a representation of v on a single line.
func (v *Variable) SinglelineString() string {
	var buf bytes.Buffer
	v.writeTo(&buf, true, false, true, "")
	return buf.String()
}

// MultilineString returns a representation of v on multiple lines.
func (v *Variable) MultilineString(indent string) string {
	var buf bytes.Buffer
	v.writeTo(&buf, true, true, true, indent)
	return buf.String()
}

func (v *Variable) writeTo(buf io.Writer, top, newlines, includeType bool, indent string) {
	if v.Unreadable != "" {
		fmt.Fprintf(buf, "(unreadable %s)", v.Unreadable)
		return
	}

	if !top && v.Addr == 0 && v.Value == "" {
		if includeType && v.Type != "void" {
			fmt.Fprintf(buf, "%s nil", v.Type)
		} else {
			fmt.Fprint(buf, "nil")
		}
		return
	}

	switch v.Kind {
	case reflect.Slice:
		v.writeSliceTo(buf, newlines, includeType, indent)
	case reflect.Array:
		v.writeArrayTo(buf, newlines, includeType, indent)
	case reflect.Ptr:
		if v.Type == "" || len(v.Children) == 0 {
			fmt.Fprint(buf, "nil")
		} else if v.Children[0].OnlyAddr && v.Children[0].Addr != 0 {
			if strings.Contains(v.Type, "/") {
				fmt.Fprintf(buf, "(%q)(%#x)", v.Type, v.Children[0].Addr)
			} else {
				fmt.Fprintf(buf, "(%s)(%#x)", v.Type, v.Children[0].Addr)
			}
		} else {
			fmt.Fprint(buf, "*")
			v.Children[0].writeTo(buf, false, newlines, includeType, indent)
		}
	case reflect.UnsafePointer:
		if len(v.Children) == 0 {
			fmt.Fprintf(buf, "unsafe.Pointer(nil)")
		} else {
			fmt.Fprintf(buf, "unsafe.Pointer(%#x)", v.Children[0].Addr)
		}
	case reflect.String:
		v.writeStringTo(buf)
	case reflect.Chan:
		if newlines {
			v.writeStructTo(buf, newlines, includeType, indent)
		} else {
			if len(v.Children) == 0 {
				fmt.Fprintf(buf, "%s nil", v.Type)
			} else {
				fmt.Fprintf(buf, "%s %s/%s", v.Type, v.Children[0].Value, v.Children[1].Value)
			}
		}
	case reflect.Struct:
		v.writeStructTo(buf, newlines, includeType, indent)
	case reflect.Interface:
		if v.Addr == 0 {
			// an escaped interface variable that points to nil, this shouldn't
			// happen in normal code but can happen if the variable is out of scope.
			fmt.Fprintf(buf, "nil")
			return
		}
		if includeType {
			if v.Children[0].Kind == reflect.Invalid {
				fmt.Fprintf(buf, "%s ", v.Type)
				if v.Children[0].Addr == 0 {
					fmt.Fprint(buf, "nil")
					return
				}
			} else {
				fmt.Fprintf(buf, "%s(%s) ", v.Type, v.Children[0].Type)
			}
		}
		data := v.Children[0]
		if data.Kind == reflect.Ptr {
			if len(data.Children) == 0 {
				fmt.Fprint(buf, "...")
			} else if data.Children[0].Addr == 0 {
				fmt.Fprint(buf, "nil")
			} else if data.Children[0].OnlyAddr {
				fmt.Fprintf(buf, "0x%x", v.Children[0].Addr)
			} else {
				v.Children[0].writeTo(buf, false, newlines, !includeType, indent)
			}
		} else if data.OnlyAddr {
			if strings.Contains(v.Type, "/") {
				fmt.Fprintf(buf, "*(*%q)(%#x)", v.Type, v.Addr)
			} else {
				fmt.Fprintf(buf, "*(*%s)(%#x)", v.Type, v.Addr)
			}
		} else {
			v.Children[0].writeTo(buf, false, newlines, !includeType, indent)
		}
	case reflect.Map:
		v.writeMapTo(buf, newlines, includeType, indent)
	case reflect.Func:
		if v.Value == "" {
			fmt.Fprint(buf, "nil")
		} else {
			fmt.Fprintf(buf, "%s", v.Value)
		}
	case reflect.Complex64, reflect.Complex128:
		fmt.Fprintf(buf, "(%s + %si)", v.Children[0].Value, v.Children[1].Value)
	default:
		if v.Value != "" {
			buf.Write([]byte(v.Value))
		} else {
			fmt.Fprintf(buf, "(unknown %s)", v.Kind)
		}
	}
}

func (v *Variable) writeStringTo(buf io.Writer) {
	s := v.Value
	if len(s) != int(v.Len) {
		s = fmt.Sprintf("%s...+%d more", s, int(v.Len)-len(s))
	}
	fmt.Fprintf(buf, "%q", s)
}

func (v *Variable) writeSliceTo(buf io.Writer, newlines, includeType bool, indent string) {
	if includeType {
		fmt.Fprintf(buf, "%s len: %d, cap: %d, ", v.Type, v.Len, v.Cap)
	}
	if v.Base == 0 && len(v.Children) == 0 {
		fmt.Fprintf(buf, "nil")
		return
	}
	v.writeSliceOrArrayTo(buf, newlines, indent)
}

func (v *Variable) writeArrayTo(buf io.Writer, newlines, includeType bool, indent string) {
	if includeType {
		fmt.Fprintf(buf, "%s ", v.Type)
	}
	v.writeSliceOrArrayTo(buf, newlines, indent)
}

func (v *Variable) writeStructTo(buf io.Writer, newlines, includeType bool, indent string) {
	if int(v.Len) != len(v.Children) && len(v.Children) == 0 {
		if strings.Contains(v.Type, "/") {
			fmt.Fprintf(buf, "(*%q)(%#x)", v.Type, v.Addr)
		} else {
			fmt.Fprintf(buf, "(*%s)(%#x)", v.Type, v.Addr)
		}
		return
	}

	if includeType {
		fmt.Fprintf(buf, "%s ", v.Type)
	}

	nl := v.shouldNewlineStruct(newlines)

	fmt.Fprint(buf, "{")

	for i := range v.Children {
		if nl {
			fmt.Fprintf(buf, "\n%s%s", indent, indentString)
		}
		fmt.Fprintf(buf, "%s: ", v.Children[i].Name)
		v.Children[i].writeTo(buf, false, nl, true, indent+indentString)
		if i != len(v.Children)-1 || nl {
			fmt.Fprint(buf, ",")
			if !nl {
				fmt.Fprint(buf, " ")
			}
		}
	}

	if len(v.Children) != int(v.Len) {
		if nl {
			fmt.Fprintf(buf, "\n%s%s", indent, indentString)
		} else {
			fmt.Fprint(buf, ",")
		}
		fmt.Fprintf(buf, "...+%d more", int(v.Len)-len(v.Children))
	}

	fmt.Fprint(buf, "}")
}

func (v *Variable) writeMapTo(buf io.Writer, newlines, includeType bool, indent string) {
	if includeType {
		fmt.Fprintf(buf, "%s ", v.Type)
	}
	if v.Base == 0 && len(v.Children) == 0 {
		fmt.Fprintf(buf, "nil")
		return
	}

	nl := newlines && (len(v.Children) > 0)

	fmt.Fprint(buf, "[")

	for i := 0; i < len(v.Children); i += 2 {
		key := &v.Children[i]
		value := &v.Children[i+1]

		if nl {
			fmt.Fprintf(buf, "\n%s%s", indent, indentString)
		}

		key.writeTo(buf, false, false, false, indent+indentString)
		fmt.Fprint(buf, ": ")
		value.writeTo(buf, false, nl, false, indent+indentString)
		if i != len(v.Children)-1 || nl {
			fmt.Fprint(buf, ", ")
		}
	}

	if len(v.Children)/2 != int(v.Len) {
		if len(v.Children) != 0 {
			if nl {
				fmt.Fprintf(buf, "\n%s%s", indent, indentString)
			} else {
				fmt.Fprint(buf, ",")
			}
			fmt.Fprintf(buf, "...+%d more", int(v.Len)-(len(v.Children)/2))
		} else {
			fmt.Fprint(buf, "...")
		}
	}

	if nl {
		fmt.Fprintf(buf, "\n%s", indent)
	}
	fmt.Fprint(buf, "]")
}

func (v *Variable) shouldNewlineArray(newlines bool) bool {
	if !newlines || len(v.Children) == 0 {
		return false
	}

	kind, hasptr := (&v.Children[0]).recursiveKind()

	switch kind {
	case reflect.Slice, reflect.Array, reflect.Struct, reflect.Map, reflect.Interface:
		return true
	case reflect.String:
		if hasptr {
			return true
		}
		for i := range v.Children {
			if len(v.Children[i].Value) > maxShortStringLen {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (v *Variable) recursiveKind() (reflect.Kind, bool) {
	hasptr := false
	var kind reflect.Kind
	for {
		kind = v.Kind
		if kind == reflect.Ptr {
			hasptr = true
			if len(v.Children) == 0 {
				return kind, hasptr
			}
			v = &(v.Children[0])
		} else {
			break
		}
	}
	return kind, hasptr
}

func (v *Variable) shouldNewlineStruct(newlines bool) bool {
	if !newlines || len(v.Children) == 0 {
		return false
	}

	for i := range v.Children {
		kind, hasptr := (&v.Children[i]).recursiveKind()

		switch kind {
		case reflect.Slice, reflect.Array, reflect.Struct, reflect.Map, reflect.Interface:
			return true
		case reflect.String:
			if hasptr {
				return true
			}
			if len(v.Children[i].Value) > maxShortStringLen {
				return true
			}
		}
	}

	return false
}

func (v *Variable) writeSliceOrArrayTo(buf io.Writer, newlines bool, indent string) {
	nl := v.shouldNewlineArray(newlines)
	fmt.Fprint(buf, "[")

	for i := range v.Children {
		if nl {
			fmt.Fprintf(buf, "\n%s%s", indent, indentString)
		}
		v.Children[i].writeTo(buf, false, nl, false, indent+indentString)
		if i != len(v.Children)-1 || nl {
			fmt.Fprint(buf, ",")
		}
	}

	if len(v.Children) != int(v.Len) {
		if len(v.Children) != 0 {
			if nl {
				fmt.Fprintf(buf, "\n%s%s", indent, indentString)
			} else {
				fmt.Fprint(buf, ",")
			}
			fmt.Fprintf(buf, "...+%d more", int(v.Len)-len(v.Children))
		} else {
			fmt.Fprint(buf, "...")
		}
	}

	if nl {
		fmt.Fprintf(buf, "\n%s", indent)
	}

	fmt.Fprint(buf, "]")
}

// PrettyExamineMemory examine the memory and format data
//
// `format` specifies the data format (or data type), `size` specifies size of each data,
// like 4byte integer, 1byte character, etc. `count` specifies the number of values.
func PrettyExamineMemory(address uintptr, memArea []byte, isLittleEndian bool, format byte, size int) string {

	var (
		cols      int
		colFormat string
		colBytes  = size

		addrLen int
		addrFmt string
	)

	// Diffrent versions of golang output differently about '#'.
	// See https://ci.appveyor.com/project/derekparker/delve-facy3/builds/30179356.
	switch format {
	case 'b':
		cols = 4 // Avoid emitting rows that are too long when using binary format
		colFormat = fmt.Sprintf("%%0%db", colBytes*8)
	case 'o':
		cols = 8
		colFormat = fmt.Sprintf("0%%0%do", colBytes*3) // Always keep one leading zero for octal.
	case 'd':
		cols = 8
		colFormat = fmt.Sprintf("%%0%dd", colBytes*3)
	case 'x':
		cols = 8
		colFormat = fmt.Sprintf("0x%%0%dx", colBytes*2) // Always keep one leading '0x' for hex.
	default:
		return fmt.Sprintf("not supprted format %q\n", string(format))
	}
	colFormat += "\t"

	l := len(memArea)
	rows := l / (cols * colBytes)
	if l%(cols*colBytes) != 0 {
		rows++
	}

	// Avoid the lens of two adjacent address are different, so always use the last addr's len to format.
	if l != 0 {
		addrLen = len(fmt.Sprintf("%x", uint64(address)+uint64(l)))
	}
	addrFmt = "0x%0" + strconv.Itoa(addrLen) + "x:\t"

	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)

	for i := 0; i < rows; i++ {
		fmt.Fprintf(w, addrFmt, address)

		for j := 0; j < cols; j++ {
			offset := i*(cols*colBytes) + j*colBytes
			if offset+colBytes <= len(memArea) {
				n := byteArrayToUInt64(memArea[offset:offset+colBytes], isLittleEndian)
				fmt.Fprintf(w, colFormat, n)
			}
		}
		fmt.Fprintln(w, "")
		address += uintptr(cols * colBytes)
	}
	w.Flush()
	return b.String()
}

func byteArrayToUInt64(buf []byte, isLittleEndian bool) uint64 {
	var n uint64
	if isLittleEndian {
		for i := len(buf) - 1; i >= 0; i-- {
			n = n<<8 + uint64(buf[i])
		}
	} else {
		for i := 0; i < len(buf); i++ {
			n = n<<8 + uint64(buf[i])
		}
	}
	return n
}
