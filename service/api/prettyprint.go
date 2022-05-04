package api

import (
	"bytes"
	"fmt"
	"io"
	"math"
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
	v.writeTo(&buf, true, false, true, "", "")
	return buf.String()
}

// SinglelineStringFormatted returns a representation of v on a single line, using the format specified by fmtstr.
func (v *Variable) SinglelineStringFormatted(fmtstr string) string {
	var buf bytes.Buffer
	v.writeTo(&buf, true, false, true, "", fmtstr)
	return buf.String()
}

// MultilineString returns a representation of v on multiple lines.
func (v *Variable) MultilineString(indent, fmtstr string) string {
	var buf bytes.Buffer
	v.writeTo(&buf, true, true, true, indent, fmtstr)
	return buf.String()
}

func (v *Variable) writeTo(buf io.Writer, top, newlines, includeType bool, indent, fmtstr string) {
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
		v.writeSliceTo(buf, newlines, includeType, indent, fmtstr)
	case reflect.Array:
		v.writeArrayTo(buf, newlines, includeType, indent, fmtstr)
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
			v.Children[0].writeTo(buf, false, newlines, includeType, indent, fmtstr)
		}
	case reflect.UnsafePointer:
		if len(v.Children) == 0 {
			fmt.Fprintf(buf, "unsafe.Pointer(nil)")
		} else {
			fmt.Fprintf(buf, "unsafe.Pointer(%#x)", v.Children[0].Addr)
		}
	case reflect.Chan:
		if newlines {
			v.writeStructTo(buf, newlines, includeType, indent, fmtstr)
		} else {
			if len(v.Children) == 0 {
				fmt.Fprintf(buf, "%s nil", v.Type)
			} else {
				fmt.Fprintf(buf, "%s %s/%s", v.Type, v.Children[0].Value, v.Children[1].Value)
			}
		}
	case reflect.Struct:
		if v.Value != "" {
			fmt.Fprintf(buf, "%s(%s)", v.Type, v.Value)
			includeType = false
		}
		v.writeStructTo(buf, newlines, includeType, indent, fmtstr)
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
				v.Children[0].writeTo(buf, false, newlines, !includeType, indent, fmtstr)
			}
		} else if data.OnlyAddr {
			if strings.Contains(v.Type, "/") {
				fmt.Fprintf(buf, "*(*%q)(%#x)", v.Type, v.Addr)
			} else {
				fmt.Fprintf(buf, "*(*%s)(%#x)", v.Type, v.Addr)
			}
		} else {
			v.Children[0].writeTo(buf, false, newlines, !includeType, indent, fmtstr)
		}
	case reflect.Map:
		v.writeMapTo(buf, newlines, includeType, indent, fmtstr)
	case reflect.Func:
		if v.Value == "" {
			fmt.Fprint(buf, "nil")
		} else {
			fmt.Fprintf(buf, "%s", v.Value)
		}
	default:
		v.writeBasicType(buf, fmtstr)
	}
}

func (v *Variable) writeBasicType(buf io.Writer, fmtstr string) {
	if v.Value == "" && v.Kind != reflect.String {
		fmt.Fprintf(buf, "(unknown %s)", v.Kind)
		return
	}

	switch v.Kind {
	case reflect.Bool:
		if fmtstr == "" {
			buf.Write([]byte(v.Value))
			return
		}
		var b bool = v.Value == "true"
		fmt.Fprintf(buf, fmtstr, b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if fmtstr == "" {
			buf.Write([]byte(v.Value))
			return
		}
		n, _ := strconv.ParseInt(v.Value, 10, 64)
		fmt.Fprintf(buf, fmtstr, n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if fmtstr == "" {
			buf.Write([]byte(v.Value))
			return
		}
		n, _ := strconv.ParseUint(v.Value, 10, 64)
		fmt.Fprintf(buf, fmtstr, n)

	case reflect.Float32, reflect.Float64:
		if fmtstr == "" {
			buf.Write([]byte(v.Value))
			return
		}
		x, _ := strconv.ParseFloat(v.Value, 64)
		fmt.Fprintf(buf, fmtstr, x)

	case reflect.Complex64, reflect.Complex128:
		if fmtstr == "" {
			fmt.Fprintf(buf, "(%s + %si)", v.Children[0].Value, v.Children[1].Value)
			return
		}
		real, _ := strconv.ParseFloat(v.Children[0].Value, 64)
		imag, _ := strconv.ParseFloat(v.Children[1].Value, 64)
		var x complex128 = complex(real, imag)
		fmt.Fprintf(buf, fmtstr, x)

	case reflect.String:
		if fmtstr == "" {
			s := v.Value
			if len(s) != int(v.Len) {
				s = fmt.Sprintf("%s...+%d more", s, int(v.Len)-len(s))
			}
			fmt.Fprintf(buf, "%q", s)
			return
		}
		fmt.Fprintf(buf, fmtstr, v.Value)
	}
}

func (v *Variable) writeSliceTo(buf io.Writer, newlines, includeType bool, indent, fmtstr string) {
	if includeType {
		fmt.Fprintf(buf, "%s len: %d, cap: %d, ", v.Type, v.Len, v.Cap)
	}
	if v.Base == 0 && len(v.Children) == 0 {
		fmt.Fprintf(buf, "nil")
		return
	}
	v.writeSliceOrArrayTo(buf, newlines, indent, fmtstr)
}

func (v *Variable) writeArrayTo(buf io.Writer, newlines, includeType bool, indent, fmtstr string) {
	if includeType {
		fmt.Fprintf(buf, "%s ", v.Type)
	}
	v.writeSliceOrArrayTo(buf, newlines, indent, fmtstr)
}

func (v *Variable) writeStructTo(buf io.Writer, newlines, includeType bool, indent, fmtstr string) {
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
		v.Children[i].writeTo(buf, false, nl, true, indent+indentString, fmtstr)
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

func (v *Variable) writeMapTo(buf io.Writer, newlines, includeType bool, indent, fmtstr string) {
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

		key.writeTo(buf, false, false, false, indent+indentString, fmtstr)
		fmt.Fprint(buf, ": ")
		value.writeTo(buf, false, nl, false, indent+indentString, fmtstr)
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

func (v *Variable) writeSliceOrArrayTo(buf io.Writer, newlines bool, indent, fmtstr string) {
	nl := v.shouldNewlineArray(newlines)
	fmt.Fprint(buf, "[")

	for i := range v.Children {
		if nl {
			fmt.Fprintf(buf, "\n%s%s", indent, indentString)
		}
		v.Children[i].writeTo(buf, false, nl, false, indent+indentString, fmtstr)
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

	// Different versions of golang output differently about '#'.
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

const stacktraceTruncatedMessage = "(truncated)"

func digits(n int) int {
	if n <= 0 {
		return 1
	}
	return int(math.Floor(math.Log10(float64(n)))) + 1
}

func PrintStack(formatPath func(string) string, out io.Writer, stack []Stackframe, ind string, offsets bool, include func(Stackframe) bool) {
	if len(stack) == 0 {
		return
	}

	extranl := offsets
	for i := range stack {
		if extranl {
			break
		}
		extranl = extranl || (len(stack[i].Defers) > 0) || (len(stack[i].Arguments) > 0) || (len(stack[i].Locals) > 0)
	}

	d := digits(len(stack) - 1)
	fmtstr := "%s%" + strconv.Itoa(d) + "d  0x%016x in %s\n"
	s := ind + strings.Repeat(" ", d+2+len(ind))

	for i := range stack {
		if !include(stack[i]) {
			continue
		}
		if stack[i].Err != "" {
			fmt.Fprintf(out, "%serror: %s\n", s, stack[i].Err)
			continue
		}
		fmt.Fprintf(out, fmtstr, ind, i, stack[i].PC, stack[i].Function.Name())
		fmt.Fprintf(out, "%sat %s:%d\n", s, formatPath(stack[i].File), stack[i].Line)

		if offsets {
			fmt.Fprintf(out, "%sframe: %+#x frame pointer %+#x\n", s, stack[i].FrameOffset, stack[i].FramePointerOffset)
		}

		for j, d := range stack[i].Defers {
			deferHeader := fmt.Sprintf("%s    defer %d: ", s, j+1)
			s2 := strings.Repeat(" ", len(deferHeader))
			if d.Unreadable != "" {
				fmt.Fprintf(out, "%s(unreadable defer: %s)\n", deferHeader, d.Unreadable)
				continue
			}
			fmt.Fprintf(out, "%s%#016x in %s\n", deferHeader, d.DeferredLoc.PC, d.DeferredLoc.Function.Name())
			fmt.Fprintf(out, "%sat %s:%d\n", s2, formatPath(d.DeferredLoc.File), d.DeferredLoc.Line)
			fmt.Fprintf(out, "%sdeferred by %s at %s:%d\n", s2, d.DeferLoc.Function.Name(), formatPath(d.DeferLoc.File), d.DeferLoc.Line)
		}

		for j := range stack[i].Arguments {
			fmt.Fprintf(out, "%s    %s = %s\n", s, stack[i].Arguments[j].Name, stack[i].Arguments[j].SinglelineString())
		}
		for j := range stack[i].Locals {
			fmt.Fprintf(out, "%s    %s = %s\n", s, stack[i].Locals[j].Name, stack[i].Locals[j].SinglelineString())
		}

		if extranl {
			fmt.Fprintln(out)
		}
	}

	if len(stack) > 0 && !stack[len(stack)-1].Bottom {
		fmt.Fprintf(out, "%s"+stacktraceTruncatedMessage+"\n", ind)
	}
}
