package godwarf

import (
	"fmt"
	"math/bits"
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
	szr := bits.OnesCount64(uint64(byteSize^(byteSize-1))) - 1 // position of rightmost 1 bit, minus 1

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
