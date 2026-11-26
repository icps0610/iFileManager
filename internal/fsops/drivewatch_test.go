package fsops

import (
	"reflect"
	"testing"
)

func TestDiffDriveMask(t *testing.T) {
	const c, d, e, f, z = 1 << 2, 1 << 3, 1 << 4, 1 << 5, 1 << 25
	cases := []struct {
		name           string
		before, after  uint32
		added, removed []string
	}{
		{"沒有變化", c | d, c | d, nil, nil},
		{"插入 USB", c | d, c | d | e, []string{`E:\`}, nil},
		{"拔除 USB", c | d | e, c | d, nil, []string{`E:\`}},
		{"同時新增與移除", c | e, c | f | z, []string{`F:\`, `Z:\`}, []string{`E:\`}},
		{"從零開始", 0, c, []string{`C:\`}, nil},
	}
	for _, tc := range cases {
		a, r := DiffDriveMask(tc.before, tc.after)
		if !reflect.DeepEqual(a, tc.added) || !reflect.DeepEqual(r, tc.removed) {
			t.Errorf("%s：得到新增 %v 移除 %v，預期新增 %v 移除 %v", tc.name, a, r, tc.added, tc.removed)
		}
	}
}
