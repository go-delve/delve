package godwarf

import (
	"fmt"
	"reflect"
)

// FakeSliceType synthesizes a slice type with the given field type.
func FakeSliceType(fieldType Type) Type {
	return &SliceType{
		StructType: StructType{
			CommonType: CommonType{
				ByteSize: 24,
				Name:     "",
			},
			StructName: "[]" + fieldType.Common().Name,
			Kind:       "struct",
			Field:      nil,
		},
		ElemType: fieldType,
	}
}

// FakeBasicType synthesizes a basic type numeric type (int8, uint16,
// float32, etc)
func FakeBasicType(name string, bitSize int) Type {
	byteSize := bitSize / 8
	szr := popcnt(uint64(byteSize^(byteSize-1))) - 1 // position of rightmost 1 bit, minus 1

	basic := func(kind reflect.Kind) BasicType {
		return BasicType{
			CommonType: CommonType{
				ByteSize:    int64(byteSize),
				Name:        fmt.Sprintf("%s%d", name, bitSize),
				ReflectKind: kind,
			},
			BitSize:   int64(bitSize),
			BitOffset: 0,
		}
	}

	switch name {
	case "int":
		return &IntType{BasicType: basic(reflect.Int8 + reflect.Kind(szr))}
	case "uint":
		return &UintType{BasicType: basic(reflect.Uint8 + reflect.Kind(szr))}
	case "float":
		return &FloatType{BasicType: basic(reflect.Float32 + reflect.Kind(szr-2))}
	case "complex":
		return &ComplexType{BasicType: basic(reflect.Complex64 + reflect.Kind(szr-3))}
	default:
		panic("unsupported")
	}
}

// popcnt is the number of bits set to 1 in x.
// It's the same as math/bits.OnesCount64, copied here so that we can build
// on versions of go that don't have math/bits.
func popcnt(x uint64) int {
	const m0 = 0x5555555555555555 // 01010101 ...
	const m1 = 0x3333333333333333 // 00110011 ...
	const m2 = 0x0f0f0f0f0f0f0f0f // 00001111 ...
	const m = 1<<64 - 1
	x = x>>1&(m0&m) + x&(m0&m)
	x = x>>2&(m1&m) + x&(m1&m)
	x = (x>>4 + x) & (m2 & m)
	x += x >> 8
	x += x >> 16
	x += x >> 32
	return int(x) & (1<<7 - 1)
}
