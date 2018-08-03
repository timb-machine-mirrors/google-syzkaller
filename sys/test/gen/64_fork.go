// AUTOGENERATED FILE
// +build !syz_target syz_target,syz_os_test,syz_arch_64_fork

package gen

import . "github.com/google/syzkaller/prog"
import . "github.com/google/syzkaller/sys/test"

func init() {
	RegisterTarget(&Target{OS: "test", Arch: "64_fork", Revision: revision_64_fork, PtrSize: 8, PageSize: 8192, NumPages: 2048, DataOffset: 536870912, Syscalls: syscalls_64_fork, Resources: resources_64_fork, Structs: structDescs_64_fork, Consts: consts_64_fork}, InitTarget)
}

var resources_64_fork = []*ResourceDesc(nil)

var structDescs_64_fork = []*KeyedStruct{
	{Key: StructKey{Name: "align0"}, Desc: &StructDesc{TypeCommon: TypeCommon{TypeName: "align0", TypeSize: 24}, Fields: []Type{
		&IntType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "int16", FldName: "f0", TypeSize: 2}}},
		&ConstType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "pad", TypeSize: 2}}, IsPad: true},
		&IntType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "int32", FldName: "f1", TypeSize: 4}}},
		&IntType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "int8", FldName: "f2", TypeSize: 1}}},
		&ConstType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "pad", TypeSize: 1}}, IsPad: true},
		&IntType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "int16", FldName: "f3", TypeSize: 2}}},
		&ConstType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "pad", TypeSize: 4}}, IsPad: true},
		&IntType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "int64", FldName: "f4", TypeSize: 8}}},
	}}},
	{Key: StructKey{Name: "compare_data"}, Desc: &StructDesc{TypeCommon: TypeCommon{TypeName: "compare_data", IsVarlen: true}, Fields: []Type{
		&StructType{Key: StructKey{Name: "align0"}, FldName: "align0"},
	}}},
}

var syscalls_64_fork = []*Syscall{
	{Name: "syz_compare", CallName: "syz_compare", Args: []Type{
		&PtrType{TypeCommon: TypeCommon{TypeName: "ptr", FldName: "want", TypeSize: 8}, Type: &BufferType{TypeCommon: TypeCommon{TypeName: "string", IsVarlen: true}, Kind: 2}},
		&LenType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "len", FldName: "want_len", TypeSize: 8}}, Buf: "want"},
		&PtrType{TypeCommon: TypeCommon{TypeName: "ptr", FldName: "got", TypeSize: 8}, Type: &UnionType{Key: StructKey{Name: "compare_data"}}},
		&LenType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "len", FldName: "got_len", TypeSize: 8}}, Buf: "got"},
	}},
	{Name: "syz_errno", CallName: "syz_errno", Args: []Type{
		&IntType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "int32", FldName: "v", TypeSize: 4}}},
	}},
	{Name: "syz_mmap", CallName: "syz_mmap", Args: []Type{
		&VmaType{TypeCommon: TypeCommon{TypeName: "vma", FldName: "addr", TypeSize: 8}},
		&LenType{IntTypeCommon: IntTypeCommon{TypeCommon: TypeCommon{TypeName: "len", FldName: "len", TypeSize: 8}}, Buf: "addr"},
	}},
}

var consts_64_fork = []ConstValue{
	{Name: "IPPROTO_ICMPV6", Value: 58},
	{Name: "IPPROTO_TCP", Value: 6},
	{Name: "IPPROTO_UDP", Value: 17},
}

const revision_64_fork = "39c2288dd1c825ce7a587f946cfc91e0e453cf5e"
