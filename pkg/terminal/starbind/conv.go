package starbind

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"go.starlark.net/starlark"

	"github.com/go-delve/delve/service/api"
)

// autoLoadConfig is the load configuration used to automatically load more from a variable
var autoLoadConfig = api.LoadConfig{false, 1, 1024, 64, -1}

// interfaceToStarlarkValue converts an interface{} variable (produced by
// decoding JSON) into a starlark.Value.
func (env *Env) interfaceToStarlarkValue(v interface{}) starlark.Value {
	switch v := v.(type) {
	case uint8:
		return starlark.MakeUint64(uint64(v))
	case uint16:
		return starlark.MakeUint64(uint64(v))
	case uint32:
		return starlark.MakeUint64(uint64(v))
	case uint64:
		return starlark.MakeUint64(v)
	case uintptr:
		return starlark.MakeUint64(uint64(v))
	case uint:
		return starlark.MakeUint64(uint64(v))
	case int8:
		return starlark.MakeInt64(int64(v))
	case int16:
		return starlark.MakeInt64(int64(v))
	case int32:
		return starlark.MakeInt64(int64(v))
	case int64:
		return starlark.MakeInt64(v)
	case int:
		return starlark.MakeInt64(int64(v))
	case string:
		return starlark.String(v)
	case map[string]uint64:
		// this is the only map type that we use in the api, if we ever want to
		// add more maps to the api a more general approach will be necessary.
		var r starlark.Dict
		for k, v := range v {
			r.SetKey(starlark.String(k), starlark.MakeUint64(v))
		}
		return &r
	case nil:
		return starlark.None
	case error:
		return starlark.String(v.Error())
	default:
		vval := reflect.ValueOf(v)
		switch vval.Type().Kind() {
		case reflect.Ptr:
			if vval.IsNil() {
				return starlark.None
			}
			vval = vval.Elem()
			if vval.Type().Kind() == reflect.Struct {
				return structAsStarlarkValue{vval, env}
			}
		case reflect.Struct:
			return structAsStarlarkValue{vval, env}
		case reflect.Slice:
			return sliceAsStarlarkValue{vval, env}
		}
		return starlark.String(fmt.Sprintf("%v", v))
	}
}

// sliceAsStarlarkValue converts a reflect.Value containing a slice
// into a starlark value.
// The public methods of sliceAsStarlarkValue implement the Indexable and
// Sequence starlark interfaces.
type sliceAsStarlarkValue struct {
	v   reflect.Value
	env *Env
}

var _ starlark.Indexable = sliceAsStarlarkValue{}
var _ starlark.Sequence = sliceAsStarlarkValue{}

func (v sliceAsStarlarkValue) Freeze() {
}

func (v sliceAsStarlarkValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("not hashable")
}

func (v sliceAsStarlarkValue) String() string {
	return fmt.Sprintf("%#v", v.v)
}

func (v sliceAsStarlarkValue) Truth() starlark.Bool {
	return v.v.Len() != 0
}

func (v sliceAsStarlarkValue) Type() string {
	return v.v.Type().String()
}

func (v sliceAsStarlarkValue) Index(i int) starlark.Value {
	if i >= v.v.Len() {
		return nil
	}
	return v.env.interfaceToStarlarkValue(v.v.Index(i).Interface())
}

func (v sliceAsStarlarkValue) Len() int {
	return v.v.Len()
}

func (v sliceAsStarlarkValue) Iterate() starlark.Iterator {
	return &sliceAsStarlarkValueIterator{0, v.v, v.env}
}

type sliceAsStarlarkValueIterator struct {
	cur int
	v   reflect.Value
	env *Env
}

func (it *sliceAsStarlarkValueIterator) Done() {
}

func (it *sliceAsStarlarkValueIterator) Next(p *starlark.Value) bool {
	if it.cur >= it.v.Len() {
		return false
	}
	*p = it.env.interfaceToStarlarkValue(it.v.Index(it.cur).Interface())
	it.cur++
	return true
}

// structAsStarlarkValue converts any Go struct into a starlark.Value.
// The public methods of structAsStarlarkValue implement the
// starlark.HasAttrs interface.
type structAsStarlarkValue struct {
	v   reflect.Value
	env *Env
}

var _ starlark.HasAttrs = structAsStarlarkValue{}

func (v structAsStarlarkValue) Freeze() {
}

func (v structAsStarlarkValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("not hashable")
}

func (v structAsStarlarkValue) String() string {
	if vv, ok := v.v.Interface().(api.Variable); ok {
		return fmt.Sprintf("Variable<%s>", vv.SinglelineString())
	}
	return fmt.Sprintf("%#v", v.v)
}

func (v structAsStarlarkValue) Truth() starlark.Bool {
	return true
}

func (v structAsStarlarkValue) Type() string {
	if vv, ok := v.v.Interface().(api.Variable); ok {
		return fmt.Sprintf("Variable<%s>", vv.Type)
	}
	return v.v.Type().String()
}

func (v structAsStarlarkValue) Attr(name string) (starlark.Value, error) {
	if r, err := v.valueAttr(name); err != nil || r != nil {
		return r, err
	}
	r := v.v.FieldByName(name)
	if r == (reflect.Value{}) {
		return starlark.None, fmt.Errorf("no field named %q in %T", name, v.v.Interface())
	}
	return v.env.interfaceToStarlarkValue(r.Interface()), nil
}

func (v structAsStarlarkValue) valueAttr(name string) (starlark.Value, error) {
	if v.v.Type().Name() != "Variable" || (name != "Value" && name != "Expr") {
		return nil, nil
	}
	v2 := v.v.Interface().(api.Variable)

	if name == "Expr" {
		return starlark.String(varAddrExpr(&v2)), nil
	}

	return v.env.variableValueToStarlarkValue(&v2, true)
}

func varAddrExpr(v *api.Variable) string {
	return fmt.Sprintf("(*(*%q)(%#x))", v.Type, v.Addr)
}

func (env *Env) variableValueToStarlarkValue(v *api.Variable, top bool) (starlark.Value, error) {
	if !top && v.Addr == 0 && v.Value == "" {
		return starlark.None, nil
	}

	switch v.Kind {
	case reflect.Struct:
		if v.Len != 0 && len(v.Children) == 0 {
			return starlark.None, errors.New("value not loaded")
		}
		return structVariableAsStarlarkValue{v, env}, nil
	case reflect.Slice, reflect.Array:
		if v.Len != 0 && len(v.Children) == 0 {
			return starlark.None, errors.New("value not loaded")
		}
		return sliceVariableAsStarlarkValue{v, env}, nil
	case reflect.Map:
		if v.Len != 0 && len(v.Children) == 0 {
			return starlark.None, errors.New("value not loaded")
		}
		return mapVariableAsStarlarkValue{v, env}, nil
	case reflect.String:
		return starlark.String(v.Value), nil
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		n, _ := strconv.ParseInt(v.Value, 0, 64)
		return starlark.MakeInt64(n), nil
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint, reflect.Uintptr:
		n, _ := strconv.ParseUint(v.Value, 0, 64)
		return starlark.MakeUint64(n), nil
	case reflect.Float32, reflect.Float64:
		switch v.Value {
		case "+Inf":
			return starlark.Float(math.Inf(+1)), nil
		case "-Inf":
			return starlark.Float(math.Inf(-1)), nil
		case "NaN":
			return starlark.Float(math.NaN()), nil
		default:
			n, _ := strconv.ParseFloat(v.Value, 0)
			return starlark.Float(n), nil
		}
	case reflect.Ptr, reflect.Interface:
		if len(v.Children) > 0 {
			v.Children[0] = *env.autoLoad(varAddrExpr(&v.Children[0]))
		}
		return ptrVariableAsStarlarkValue{v, env}, nil
	}
	return nil, nil
}

func (env *Env) autoLoad(expr string) *api.Variable {
	v, err := env.ctx.Client().EvalVariable(api.EvalScope{-1, 0, 0}, expr, autoLoadConfig)
	if err != nil {
		return &api.Variable{Unreadable: err.Error()}
	}
	return v
}

func (v structAsStarlarkValue) AttrNames() []string {
	typ := v.v.Type()
	r := make([]string, 0, typ.NumField()+1)
	for i := 0; i < typ.NumField(); i++ {
		r = append(r, typ.Field(i).Name)
	}
	return r
}

// structVariableAsStarlarkValue converts an api.Variable representing a
// struct variable (in the target process) into a starlark.Value.
// The public methods of structVariableAsStarlarkValue implement the
// starlark.HasAttrs and starlark.Mapping interfaces.
type structVariableAsStarlarkValue struct {
	v   *api.Variable
	env *Env
}

var _ starlark.HasAttrs = structVariableAsStarlarkValue{}
var _ starlark.Mapping = structVariableAsStarlarkValue{}

func (v structVariableAsStarlarkValue) Freeze() {
}

func (v structVariableAsStarlarkValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("not hashable")
}

func (v structVariableAsStarlarkValue) String() string {
	return fmt.Sprintf("%s", v.v.SinglelineString())
}

func (v structVariableAsStarlarkValue) Truth() starlark.Bool {
	return true
}

func (v structVariableAsStarlarkValue) Type() string {
	return v.v.Type
}

func (v structVariableAsStarlarkValue) Attr(name string) (starlark.Value, error) {
	for i := range v.v.Children {
		if v.v.Children[i].Name == name {
			v2 := v.env.autoLoad(varAddrExpr(&v.v.Children[i]))
			return v.env.variableValueToStarlarkValue(v2, false)
		}
	}
	return nil, nil // no such field or method
}

func (v structVariableAsStarlarkValue) AttrNames() []string {
	r := make([]string, len(v.v.Children))
	for i := range v.v.Children {
		r[i] = v.v.Children[i].Name
	}
	return r
}

func (v structVariableAsStarlarkValue) Get(key starlark.Value) (starlark.Value, bool, error) {
	skey, ok := key.(starlark.String)
	if !ok {
		return starlark.None, false, nil
	}
	r, err := v.Attr(string(skey))
	if r == nil && err == nil {
		return starlark.None, false, nil
	}
	if err != nil {
		return starlark.None, false, err
	}
	return r, true, nil
}

type sliceVariableAsStarlarkValue struct {
	v   *api.Variable
	env *Env
}

var _ starlark.Indexable = sliceVariableAsStarlarkValue{}
var _ starlark.Sequence = sliceVariableAsStarlarkValue{}

func (v sliceVariableAsStarlarkValue) Freeze() {
}

func (v sliceVariableAsStarlarkValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("not hashable")
}

func (v sliceVariableAsStarlarkValue) String() string {
	return fmt.Sprintf("%s", v.v.SinglelineString())
}

func (v sliceVariableAsStarlarkValue) Truth() starlark.Bool {
	return v.v.Len != 0
}

func (v sliceVariableAsStarlarkValue) Type() string {
	return v.v.Type
}

func (v sliceVariableAsStarlarkValue) Index(i int) starlark.Value {
	if i >= v.Len() {
		return nil
	}
	v2 := v.env.autoLoad(fmt.Sprintf("%s[%d]", varAddrExpr(v.v), i))
	r, err := v.env.variableValueToStarlarkValue(v2, false)
	if err != nil {
		return starlark.String(err.Error())
	}
	return r
}

func (v sliceVariableAsStarlarkValue) Len() int {
	return int(v.v.Len)
}

func (v sliceVariableAsStarlarkValue) Iterate() starlark.Iterator {
	return &sliceVariableAsStarlarkValueIterator{0, v.v, v.env}
}

type sliceVariableAsStarlarkValueIterator struct {
	cur int64
	v   *api.Variable
	env *Env
}

func (it *sliceVariableAsStarlarkValueIterator) Done() {
}

func (it *sliceVariableAsStarlarkValueIterator) Next(p *starlark.Value) bool {
	if it.cur >= it.v.Len {
		return false
	}
	s := sliceVariableAsStarlarkValue{it.v, it.env}
	*p = s.Index(int(it.cur))
	it.cur++
	return true
}

type ptrVariableAsStarlarkValue struct {
	v   *api.Variable
	env *Env
}

var _ starlark.HasAttrs = ptrVariableAsStarlarkValue{}
var _ starlark.Mapping = ptrVariableAsStarlarkValue{}

func (v ptrVariableAsStarlarkValue) Freeze() {
}

func (v ptrVariableAsStarlarkValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("not hashable")
}

func (v ptrVariableAsStarlarkValue) String() string {
	return fmt.Sprintf("%s", v.v.SinglelineString())
}

func (v ptrVariableAsStarlarkValue) Truth() starlark.Bool {
	return true
}

func (v ptrVariableAsStarlarkValue) Type() string {
	return v.v.Type
}

func (v ptrVariableAsStarlarkValue) Attr(name string) (starlark.Value, error) {
	if len(v.v.Children) == 0 {
		return nil, nil // no such field or method
	}
	if v.v.Children[0].Kind == reflect.Struct {
		// autodereference pointers to structs
		x := structVariableAsStarlarkValue{&v.v.Children[0], v.env}
		return x.Attr(name)
	} else if v.v.Kind == reflect.Interface && v.v.Children[0].Kind == reflect.Ptr {
		// allow double-autodereference for iface to ptr to struct
		vchild := &v.v.Children[0]
		if len(vchild.Children) > 0 {
			vchild.Children[0] = *v.env.autoLoad(varAddrExpr(&vchild.Children[0]))
		}
		v2 := ptrVariableAsStarlarkValue{vchild, v.env}
		return v2.Attr(name)
	}

	return nil, nil
}

func (v ptrVariableAsStarlarkValue) AttrNames() []string {
	if v.v.Children[0].Kind != reflect.Struct {
		return nil
	}
	// autodereference
	x := structVariableAsStarlarkValue{&v.v.Children[0], v.env}
	return x.AttrNames()
}

func (v ptrVariableAsStarlarkValue) Get(key starlark.Value) (starlark.Value, bool, error) {
	if ikey, ok := key.(starlark.Int); ok {
		if len(v.v.Children) == 0 {
			return starlark.None, true, nil
		}
		if idx, _ := ikey.Int64(); idx == 0 {
			r, err := v.env.variableValueToStarlarkValue(&v.v.Children[0], false)
			if err != nil {
				return starlark.String(err.Error()), true, nil
			}
			return r, true, nil
		}
		return starlark.None, false, nil
	}

	if len(v.v.Children) == 0 || v.v.Children[0].Kind != reflect.Struct {
		return starlark.None, false, nil
	}
	// autodereference
	x := structVariableAsStarlarkValue{&v.v.Children[0], v.env}
	return x.Get(key)
}

type mapVariableAsStarlarkValue struct {
	v   *api.Variable
	env *Env
}

var _ starlark.IterableMapping = mapVariableAsStarlarkValue{}

func (v mapVariableAsStarlarkValue) Freeze() {
}

func (v mapVariableAsStarlarkValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("not hashable")
}

func (v mapVariableAsStarlarkValue) String() string {
	return fmt.Sprintf("%s", v.v.SinglelineString())
}

func (v mapVariableAsStarlarkValue) Truth() starlark.Bool {
	return true
}

func (v mapVariableAsStarlarkValue) Type() string {
	return v.v.Type
}

func (v mapVariableAsStarlarkValue) Get(key starlark.Value) (starlark.Value, bool, error) {
	var keyExpr string
	switch key := key.(type) {
	case starlark.Int:
		keyExpr = key.String()
	case starlark.Float:
		keyExpr = fmt.Sprintf("%g", float64(key))
	case starlark.String:
		keyExpr = fmt.Sprintf("%q", string(key))
	case starlark.Bool:
		keyExpr = fmt.Sprintf("%v", bool(key))
	case structVariableAsStarlarkValue:
		keyExpr = varAddrExpr(key.v)
	default:
		return starlark.None, false, fmt.Errorf("key type not supported %T", key)
	}

	v2 := v.env.autoLoad(fmt.Sprintf("%s[%s]", varAddrExpr(v.v), keyExpr))
	r, err := v.env.variableValueToStarlarkValue(v2, false)
	if err != nil {
		if err.Error() == "key not found" {
			return starlark.None, false, nil
		}
		return starlark.None, false, err
	}
	return r, true, nil
}

func (v mapVariableAsStarlarkValue) Items() []starlark.Tuple {
	r := make([]starlark.Tuple, 0, len(v.v.Children)/2)
	for i := 0; i < len(v.v.Children); i += 2 {
		r = append(r, mapStarlarkTupleAt(v.v, v.env, i))
	}
	return r
}

func mapStarlarkTupleAt(v *api.Variable, env *Env, i int) starlark.Tuple {
	keyv := env.autoLoad(varAddrExpr(&v.Children[i]))
	key, err := env.variableValueToStarlarkValue(keyv, false)
	if err != nil {
		key = starlark.None
	}
	valv := env.autoLoad(varAddrExpr(&v.Children[i+1]))
	val, err := env.variableValueToStarlarkValue(valv, false)
	if err != nil {
		val = starlark.None
	}
	return starlark.Tuple{key, val}
}

func (v mapVariableAsStarlarkValue) Iterate() starlark.Iterator {
	return &mapVariableAsStarlarkValueIterator{0, v.v, v.env}
}

type mapVariableAsStarlarkValueIterator struct {
	cur int
	v   *api.Variable
	env *Env
}

func (it *mapVariableAsStarlarkValueIterator) Done() {
}

func (it *mapVariableAsStarlarkValueIterator) Next(p *starlark.Value) bool {
	if it.cur >= 2*int(it.v.Len) {
		return false
	}
	if it.cur >= len(it.v.Children) {
		v2 := it.env.autoLoad(fmt.Sprintf("%s[%d:]", varAddrExpr(it.v), len(it.v.Children)/2))
		it.v.Children = append(it.v.Children, v2.Children...)
	}
	if it.cur >= len(it.v.Children) {
		return false
	}

	keyv := it.env.autoLoad(varAddrExpr(&it.v.Children[it.cur]))
	key, err := it.env.variableValueToStarlarkValue(keyv, false)
	if err != nil {
		key = starlark.None
	}
	*p = key

	it.cur += 2
	return true
}

// unmarshalStarlarkValue unmarshals a starlark.Value 'val' into a Go variable 'dst'.
// This works similarly to encoding/json.Unmarshal and similar functions,
// but instead of getting its input from a byte buffer, it uses a
// starlark.Value.
func unmarshalStarlarkValue(val starlark.Value, dst interface{}, path string) error {
	return unmarshalStarlarkValueIntl(val, reflect.ValueOf(dst), path)
}

func unmarshalStarlarkValueIntl(val starlark.Value, dst reflect.Value, path string) (err error) {
	defer func() {
		// catches reflect panics
		ierr := recover()
		if ierr != nil {
			err = fmt.Errorf("error setting argument %q to %s: %v", path, val, ierr)
		}
	}()

	converr := func(args ...string) error {
		if len(args) > 0 {
			return fmt.Errorf("error setting argument %q: can not convert %s to %s: %s", path, val, dst.Type().String(), args[0])
		}
		return fmt.Errorf("error setting argument %q: can not convert %s to %s", path, val, dst.Type().String())
	}

	if _, isnone := val.(starlark.NoneType); isnone {
		return nil
	}

	for dst.Kind() == reflect.Ptr {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		dst = dst.Elem()
	}

	switch val := val.(type) {
	case starlark.Bool:
		dst.SetBool(bool(val))
	case starlark.Int:
		switch dst.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			n, ok := val.Uint64()
			if !ok {
				return converr()
			}
			dst.SetUint(n)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, ok := val.Int64()
			if !ok {
				return converr()
			}
			dst.SetInt(n)
		default:
			return converr()
		}
	case starlark.Float:
		dst.SetFloat(float64(val))
	case starlark.String:
		dst.SetString(string(val))
	case *starlark.List:
		if dst.Kind() != reflect.Slice {
			return converr()
		}
		r := []reflect.Value{}
		for i := 0; i < val.Len(); i++ {
			cur := reflect.New(dst.Type().Elem())
			err := unmarshalStarlarkValueIntl(val.Index(i), cur, path)
			if err != nil {
				return err
			}
			r = append(r, cur)
		}
	case *starlark.Dict:
		if dst.Kind() != reflect.Struct {
			return converr()
		}
		for _, k := range val.Keys() {
			if _, ok := k.(starlark.String); !ok {
				return converr(fmt.Sprintf("non-string key %q", k.String()))
			}
			fieldName := string(k.(starlark.String))
			dstfield := dst.FieldByName(fieldName)
			if dstfield == (reflect.Value{}) {
				return converr(fmt.Sprintf("unknown field %s", fieldName))
			}
			valfield, _, _ := val.Get(starlark.String(fieldName))
			err := unmarshalStarlarkValueIntl(valfield, dstfield, path+"."+fieldName)
			if err != nil {
				return err
			}
		}
	case structAsStarlarkValue:
		rv := val.v
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		dst.Set(rv)
	default:
		return converr()
	}
	return nil
}
