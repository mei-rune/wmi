package wmi

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

type SliceValuer interface {
	Grow(n int)
	LoadEntity(src *ole.IDispatch) error
}

type Valuer interface {
	LoadEntity(src *ole.IDispatch) error
	GetValue() reflect.Value
}

func MustSliceValuer(dst interface{}) *reflectSliceValuer {
	sv, err := NewSliceValuer(dst)
	if err != nil {
		panic(err)
	}
	return sv
}

func NewSliceValuer(dst interface{}) (*reflectSliceValuer, error) {
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return nil, ErrInvalidEntityType
	}
	dv = dv.Elem()
	mat, elemType := checkMultiArg(dv)
	if mat == multiArgTypeInvalid {
		return nil, ErrInvalidEntityType
	}

	return &reflectSliceValuer{
		dv:       dv,
		mat:      mat,
		elemType: elemType,
	}, nil
}

type reflectSliceValuer struct {
	dv reflect.Value

	mat      multiArgType
	elemType reflect.Type

	AllowMissingFields bool
	NonePtrZero        bool
	PtrNil             bool
}

func (rv *reflectSliceValuer) Grow(n int) {
	rv.dv.Set(reflect.MakeSlice(rv.dv.Type(), 0, n))
}

func (rv *reflectSliceValuer) LoadEntity(src *ole.IDispatch) error {
	ev := reflect.New(rv.elemType)

	err := rv.loadEntity(ev.Elem(), src)
	if err != nil {
		return err
	}

	if rv.mat != multiArgTypeStructPtr {
		ev = ev.Elem()
	}
	rv.dv.Set(reflect.Append(rv.dv, ev))
	return nil
}

// loadEntity loads a SWbemObject into a struct pointer.
func (rv *reflectSliceValuer) loadEntity(ev reflect.Value, src *ole.IDispatch) (errFieldMismatch error) {
	// v := reflect.ValueOf(dst).Elem()
	for i := 0; i < ev.NumField(); i++ {		
		n := ev.Type().Field(i).Name
		if n[0] < 'A' || n[0] > 'Z' {
			continue
		}
		prop, err := oleutil.GetProperty(src, n)
		if err != nil {
			if !rv.AllowMissingFields {
				of := ev.Field(i)
				errFieldMismatch = &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "no such struct field",
				}
			}
			continue
		}
		defer prop.Clear()

		if prop.VT == 0x1 { //VT_NULL
			if !rv.PtrNil {
					f := ev.Field(i)
					isPtr := f.Kind() == reflect.Ptr
					if isPtr {
						ptr := reflect.New(f.Type().Elem())
						f.Set(ptr)
					}
			}
			continue
		}

		f := ev.Field(i)
		of := f
		isPtr := f.Kind() == reflect.Ptr
		if isPtr {
			ptr := reflect.New(f.Type().Elem())
			f.Set(ptr)
			f = f.Elem()
		}
		if !f.CanSet() {
			return &ErrFieldMismatch{
				StructType: of.Type(),
				FieldName:  n,
				Reason:     "CanSet() is false",
			}
		}

		switch val := prop.Value().(type) {
		case int8, int16, int32, int64, int:
			v := reflect.ValueOf(val).Int()
			switch f.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				f.SetInt(v)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				f.SetUint(uint64(v))
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not an integer class",
				}
			}
		case uint8, uint16, uint32, uint64:
			v := reflect.ValueOf(val).Uint()
			switch f.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				f.SetInt(int64(v))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				f.SetUint(v)
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not an integer class",
				}
			}
		case string:
			switch f.Kind() {
			case reflect.String:
				f.SetString(val)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				iv, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					return err
				}
				f.SetInt(iv)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				uv, err := strconv.ParseUint(val, 10, 64)
				if err != nil {
					return err
				}
				f.SetUint(uv)
			case reflect.Struct:
				switch f.Type() {
				case timeType:
					if len(val) == 25 {
						mins, err := strconv.Atoi(val[22:])
						if err != nil {
							return err
						}
						val = val[:22] + fmt.Sprintf("%02d%02d", mins/60, mins%60)
					}
					t, err := time.Parse("20060102150405.000000-0700", val)
					if err != nil {
						return err
					}
					f.Set(reflect.ValueOf(t))
				}
			}
		case bool:
			switch f.Kind() {
			case reflect.Bool:
				f.SetBool(val)
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not a bool",
				}
			}
		case float32:
			switch f.Kind() {
			case reflect.Float32:
				f.SetFloat(float64(val))
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not a Float32",
				}
			}
		default:
			if f.Kind() == reflect.Slice {
				switch f.Type().Elem().Kind() {
				case reflect.String:
					safeArray := prop.ToArray()
					if safeArray != nil {
						arr := safeArray.ToValueArray()
						fArr := reflect.MakeSlice(f.Type(), len(arr), len(arr))
						for i, v := range arr {
							s := fArr.Index(i)
							s.SetString(v.(string))
						}
						f.Set(fArr)
					}
				case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
					safeArray := prop.ToArray()
					if safeArray != nil {
						arr := safeArray.ToValueArray()
						fArr := reflect.MakeSlice(f.Type(), len(arr), len(arr))
						for i, v := range arr {
							s := fArr.Index(i)
							s.SetUint(reflect.ValueOf(v).Uint())
						}
						f.Set(fArr)
					}
				case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
					safeArray := prop.ToArray()
					if safeArray != nil {
						arr := safeArray.ToValueArray()
						fArr := reflect.MakeSlice(f.Type(), len(arr), len(arr))
						for i, v := range arr {
							s := fArr.Index(i)
							s.SetInt(reflect.ValueOf(v).Int())
						}
						f.Set(fArr)
					}
				default:
					return &ErrFieldMismatch{
						StructType: of.Type(),
						FieldName:  n,
						Reason:     fmt.Sprintf("unsupported slice type (%T)", val),
					}
				}
			} else {
				typeof := reflect.TypeOf(val)
				if typeof == nil && (isPtr || rv.NonePtrZero) {
					if (isPtr && rv.PtrNil) || (!isPtr && rv.NonePtrZero) {
						of.Set(reflect.Zero(of.Type()))
					}
					break
				}
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     fmt.Sprintf("unsupported type (%T)", val),
				}
			}
		}
	}
	return errFieldMismatch
}

type MapValuer struct {
	s []map[string]interface{}
}

func (m *MapValuer) Grow(n int) {
	if m.s == nil {
		m.s = make([]map[string]interface{}, 0, n)
	}
}

func (m *MapValuer) LoadEntity(src *ole.IDispatch) error {
	ev := map[string]interface{}{}
	if err := rv.loadEntity(ev, src); err != nil {
		return err
	}
	m.s = append(m.s, ev)
	return nil
}

func (v *MapValuer) loadEntity(ev map[string]interface{}, src *ole.IDispatch) error {
	// v := reflect.ValueOf(dst).Elem()
	for i := 0; i < ev.NumField(); i++ {		
		n := ev.Type().Field(i).Name
		if n[0] < 'A' || n[0] > 'Z' {
			continue
		}
		prop, err := oleutil.GetProperty(src, n)
		if err != nil {
			if !rv.AllowMissingFields {
				of := ev.Field(i)
				errFieldMismatch = &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "no such struct field",
				}
			}
			continue
		}
		defer prop.Clear()

		if prop.VT == 0x1 { //VT_NULL
			if !rv.PtrNil {
					f := ev.Field(i)
					isPtr := f.Kind() == reflect.Ptr
					if isPtr {
						ptr := reflect.New(f.Type().Elem())
						f.Set(ptr)
					}
			}
			continue
		}

		f := ev.Field(i)
		of := f
		isPtr := f.Kind() == reflect.Ptr
		if isPtr {
			ptr := reflect.New(f.Type().Elem())
			f.Set(ptr)
			f = f.Elem()
		}
		if !f.CanSet() {
			return &ErrFieldMismatch{
				StructType: of.Type(),
				FieldName:  n,
				Reason:     "CanSet() is false",
			}
		}

		switch val := prop.Value().(type) {
		case int8, int16, int32, int64, int:
			v := reflect.ValueOf(val).Int()
			switch f.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				f.SetInt(v)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				f.SetUint(uint64(v))
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not an integer class",
				}
			}
		case uint8, uint16, uint32, uint64:
			v := reflect.ValueOf(val).Uint()
			switch f.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				f.SetInt(int64(v))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				f.SetUint(v)
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not an integer class",
				}
			}
		case string:
			switch f.Kind() {
			case reflect.String:
				f.SetString(val)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				iv, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					return err
				}
				f.SetInt(iv)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				uv, err := strconv.ParseUint(val, 10, 64)
				if err != nil {
					return err
				}
				f.SetUint(uv)
			case reflect.Struct:
				switch f.Type() {
				case timeType:
					if len(val) == 25 {
						mins, err := strconv.Atoi(val[22:])
						if err != nil {
							return err
						}
						val = val[:22] + fmt.Sprintf("%02d%02d", mins/60, mins%60)
					}
					t, err := time.Parse("20060102150405.000000-0700", val)
					if err != nil {
						return err
					}
					f.Set(reflect.ValueOf(t))
				}
			}
		case bool:
			switch f.Kind() {
			case reflect.Bool:
				f.SetBool(val)
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not a bool",
				}
			}
		case float32:
			switch f.Kind() {
			case reflect.Float32:
				f.SetFloat(float64(val))
			default:
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     "not a Float32",
				}
			}
		default:
			if f.Kind() == reflect.Slice {
				switch f.Type().Elem().Kind() {
				case reflect.String:
					safeArray := prop.ToArray()
					if safeArray != nil {
						arr := safeArray.ToValueArray()
						fArr := reflect.MakeSlice(f.Type(), len(arr), len(arr))
						for i, v := range arr {
							s := fArr.Index(i)
							s.SetString(v.(string))
						}
						f.Set(fArr)
					}
				case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
					safeArray := prop.ToArray()
					if safeArray != nil {
						arr := safeArray.ToValueArray()
						fArr := reflect.MakeSlice(f.Type(), len(arr), len(arr))
						for i, v := range arr {
							s := fArr.Index(i)
							s.SetUint(reflect.ValueOf(v).Uint())
						}
						f.Set(fArr)
					}
				case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
					safeArray := prop.ToArray()
					if safeArray != nil {
						arr := safeArray.ToValueArray()
						fArr := reflect.MakeSlice(f.Type(), len(arr), len(arr))
						for i, v := range arr {
							s := fArr.Index(i)
							s.SetInt(reflect.ValueOf(v).Int())
						}
						f.Set(fArr)
					}
				default:
					return &ErrFieldMismatch{
						StructType: of.Type(),
						FieldName:  n,
						Reason:     fmt.Sprintf("unsupported slice type (%T)", val),
					}
				}
			} else {
				typeof := reflect.TypeOf(val)
				if typeof == nil && (isPtr || rv.NonePtrZero) {
					if (isPtr && rv.PtrNil) || (!isPtr && rv.NonePtrZero) {
						of.Set(reflect.Zero(of.Type()))
					}
					break
				}
				return &ErrFieldMismatch{
					StructType: of.Type(),
					FieldName:  n,
					Reason:     fmt.Sprintf("unsupported type (%T)", val),
				}
			}
		}
	}
	return errFieldMismatch
}