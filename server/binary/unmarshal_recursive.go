package binary

import (
	"golang.org/x/xerrors"
)

type RawObj struct {
	ClassId byte
	Body    interface{}
}

// UnmarshalRecursive unmarshal all bytes recursive
func UnmarshalRecursive(src []byte) (interface{}, error) {
	if len(src) == 0 {
		return nil, xerrors.Errorf("Unmarshal error: empty")
	}
	u, n, err := unmarshalRecursive(src)
	if err != nil {
		return nil, err
	}
	if n >= len(src) {
		return u, nil
	}

	r := []interface{}{u}
	src = src[n:]
	for len(src) > 0 {
		u, n, err = unmarshalRecursive(src)
		if err != nil {
			return nil, err
		}
		r = append(r, u)
		src = src[n:]
	}

	return r, nil
}

func unmarshalRecursive(src []byte) (interface{}, int, error) {
	u, n, err := Unmarshal(src)
	if err != nil {
		return nil, n, err
	}

	switch v := u.(type) {
	case *Obj:
		o := RawObj{
			ClassId: v.ClassId,
		}

		if len(v.Body) == 0 {
			return o, n, nil
		}
		b, err := UnmarshalRecursive(v.Body)
		if err != nil {
			return o, n, err
		}
		o.Body = b
		return o, n, err
	case Dict:
		o := make(map[string]interface{})
		for k, v := range v {
			u, err := UnmarshalRecursive(v)
			if err != nil {
				return nil, n, err
			}
			o[k] = u
		}
		return o, n, nil
	case List:
		o := make([]interface{}, 0)
		for i := 0; i < len(v); i++ {
			u, err := UnmarshalRecursive(v[i])
			if err != nil {
				return nil, n, err
			}
			o = append(o, u)
		}
		return o, n, nil
	default:
		return v, n, nil
	}
}
