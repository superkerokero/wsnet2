package lobby

import (
	"bytes"

	"golang.org/x/xerrors"

	"wsnet2/binary"
	"wsnet2/log"
)

//go:generate stringer -type=OpType
type OpType byte

const (
	OpEqual OpType = iota
	OpNot
	OpLessThan
	OpLessThanOrEqual
	OpGreaterThan
	OpGreaterThanOrEqual
	OpContain
	OpNotContain
)

type PropQuery struct {
	Key string
	Op  OpType
	Val []byte
}

func unmarshalProps(props []byte) (binary.Dict, error) {
	um, _, err := binary.Unmarshal(props)
	if err != nil {
		return nil, err
	}
	dict, ok := um.(binary.Dict)
	if !ok {
		return nil, xerrors.Errorf("type is not Dict: %v", binary.Type(props[0]))
	}
	return dict, nil
}

func (q *PropQuery) match(val []byte) (bool, error) {
	if q.Op == OpContain || q.Op == OpNotContain {
		return q.contain(val), nil
	}

	ret := bytes.Compare(val, q.Val)
	switch q.Op {
	case OpEqual:
		return ret == 0, nil
	case OpNot:
		return ret != 0, nil
	case OpLessThan:
		return ret < 0, nil
	case OpLessThanOrEqual:
		return ret <= 0, nil
	case OpGreaterThan:
		return ret > 0, nil
	case OpGreaterThanOrEqual:
		return ret >= 0, nil
	}

	return false, xerrors.Errorf("unsupported operator: %v (%s)", q.Op, q.Key)
}

func (q *PropQuery) containBool(val []byte) bool {
	qv, _, e := binary.UnmarshalAs(q.Val, binary.TypeTrue, binary.TypeFalse)
	if e != nil {
		return q.Op == OpNotContain
	}
	qval := qv.(bool)

	list, _, e := binary.UnmarshalAs(val, binary.TypeBools)
	if e != nil {
		return q.Op == OpNotContain
	}

	for _, v := range list.([]bool) {
		if v == qval {
			return q.Op == OpContain
		}
	}

	return q.Op == OpNotContain
}

func (q *PropQuery) containNum(val []byte, elemType binary.Type) bool {
	queryType := binary.Type(q.Val[0])
	if elemType != queryType {
		log.Debugf("containNum: type mismatch: query=%v, list=%v", queryType, binary.Type(val[0]))
		return q.Op == OpNotContain
	}
	elemSize := binary.NumTypeDataSize[elemType]
	hdrSize := 3       // Type byte + length(16bit)
	qData := q.Val[1:] // remove Type byte
	for i := hdrSize; i < len(val); i += elemSize {
		if bytes.Equal(val[i:i+elemSize], qData) {
			return q.Op == OpContain
		}
	}
	return q.Op == OpNotContain
}

func (q *PropQuery) contain(val []byte) bool {
	listtype := binary.Type(val[0])
	switch listtype {
	case binary.TypeNull:
		return q.Op == OpNotContain
	case binary.TypeList:
		l, _, e := binary.UnmarshalAs(val, binary.TypeList)
		if e != nil {
			return q.Op == OpNotContain
		}
		for _, v := range l.(binary.List) {
			if bytes.Equal(v, q.Val) {
				return q.Op == OpContain
			}
		}
		return q.Op == OpNotContain
	case binary.TypeBools:
		return q.containBool(val)
	default:
		elemtype, ok := binary.NumListElementType[listtype]
		if ok {
			return q.containNum(val, elemtype)
		}
	}

	log.Errorf("PropQuery.contain: property is not a list: %v", listtype)
	return false
}

type PropQueries []PropQuery

func (pqs *PropQueries) match(props binary.Dict) (bool, error) {
	for _, q := range *pqs {
		match, err := q.match(props[q.Key])
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}
