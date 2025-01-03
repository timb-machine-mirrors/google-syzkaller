// Copyright 2017 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

// Package compiler generates sys descriptions of syscalls, types and resources
// from textual descriptions.
package compiler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/syzkaller/pkg/ast"
	"github.com/google/syzkaller/prog"
	"github.com/google/syzkaller/sys/targets"
)

// Overview of compilation process:
// 1. ast.Parse on text file does tokenization and builds AST.
//    This step catches basic syntax errors. AST contains full debug info.
// 2. ExtractConsts as AST returns set of constant identifiers.
//    This step also does verification of include/incdir/define AST nodes.
// 3. User translates constants to values.
// 4. Compile on AST and const values does the rest of the work and returns Prog
//    containing generated prog objects.
// 4.1. assignSyscallNumbers: uses consts to assign syscall numbers.
//      This step also detects unsupported syscalls and discards no longer
//      needed AST nodes (inlcude, define, comments, etc).
// 4.2. patchConsts: patches Int nodes referring to consts with corresponding values.
//      Also detects unsupported syscalls, structs, resources due to missing consts.
// 4.3. check: does extensive semantical checks of AST.
// 4.4. gen: generates prog objects from AST.

// Prog is description compilation result.
type Prog struct {
	Resources []*prog.ResourceDesc
	Syscalls  []*prog.Syscall
	Types     []prog.Type
	// Set of unsupported syscalls/flags.
	Unsupported map[string]bool
	// Returned if consts was nil.
	fileConsts map[string]*ConstInfo
}

func createCompiler(desc *ast.Description, target *targets.Target, eh ast.ErrorHandler) *compiler {
	if eh == nil {
		eh = ast.LoggingHandler
	}
	desc.Nodes = append(builtinDescs.Clone().Nodes, desc.Nodes...)
	comp := &compiler{
		desc:           desc,
		target:         target,
		eh:             eh,
		ptrSize:        target.PtrSize,
		unsupported:    make(map[string]bool),
		resources:      make(map[string]*ast.Resource),
		typedefs:       make(map[string]*ast.TypeDef),
		structs:        make(map[string]*ast.Struct),
		intFlags:       make(map[string]*ast.IntFlags),
		strFlags:       make(map[string]*ast.StrFlags),
		used:           make(map[string]bool),
		usedTypedefs:   make(map[string]bool),
		brokenTypedefs: make(map[string]bool),
		structVarlen:   make(map[string]bool),
		structTypes:    make(map[string]prog.Type),
		structFiles:    make(map[*ast.Struct]map[string]ast.Pos),
		recursiveQuery: make(map[ast.Node]bool),
		builtinConsts: map[string]uint64{
			"PTR_SIZE": target.PtrSize,
		},
	}
	return comp
}

// Compile compiles sys description.
func Compile(desc *ast.Description, consts map[string]uint64, target *targets.Target, eh ast.ErrorHandler) *Prog {
	comp := createCompiler(desc.Clone(), target, eh)
	comp.filterArch()
	comp.typecheck()
	comp.flattenFlags()
	// The subsequent, more complex, checks expect basic validity of the tree,
	// in particular corrent number of type arguments. If there were errors,
	// don't proceed to avoid out-of-bounds references to type arguments.
	if comp.errors != 0 {
		return nil
	}
	if consts == nil {
		fileConsts := comp.extractConsts()
		if comp.errors != 0 {
			return nil
		}
		return &Prog{fileConsts: fileConsts}
	}
	if comp.target.SyscallNumbers {
		comp.assignSyscallNumbers(consts)
	}
	comp.patchConsts(consts)
	comp.check(consts)
	if comp.errors != 0 {
		return nil
	}
	syscalls := comp.genSyscalls()
	comp.layoutTypes(syscalls)
	types := comp.generateTypes(syscalls)
	prg := &Prog{
		Resources:   comp.genResources(),
		Syscalls:    syscalls,
		Types:       types,
		Unsupported: comp.unsupported,
	}
	if comp.errors != 0 {
		return nil
	}
	for _, w := range comp.warnings {
		eh(w.pos, w.msg)
	}
	return prg
}

type compiler struct {
	desc     *ast.Description
	target   *targets.Target
	eh       ast.ErrorHandler
	errors   int
	warnings []warn
	ptrSize  uint64

	unsupported    map[string]bool
	resources      map[string]*ast.Resource
	typedefs       map[string]*ast.TypeDef
	structs        map[string]*ast.Struct
	intFlags       map[string]*ast.IntFlags
	strFlags       map[string]*ast.StrFlags
	used           map[string]bool // contains used structs/resources
	usedTypedefs   map[string]bool
	brokenTypedefs map[string]bool

	structVarlen   map[string]bool
	structTypes    map[string]prog.Type
	structFiles    map[*ast.Struct]map[string]ast.Pos
	builtinConsts  map[string]uint64
	fileMetas      map[string]Meta
	recursiveQuery map[ast.Node]bool
}

type warn struct {
	pos ast.Pos
	msg string
}

func (comp *compiler) error(pos ast.Pos, msg string, args ...interface{}) {
	comp.errors++
	comp.eh(pos, fmt.Sprintf(msg, args...))
}

func (comp *compiler) warning(pos ast.Pos, msg string, args ...interface{}) {
	comp.warnings = append(comp.warnings, warn{pos, fmt.Sprintf(msg, args...)})
}

func (comp *compiler) filterArch() {
	comp.desc = comp.desc.Filter(func(n ast.Node) bool {
		pos, typ, name := n.Info()
		if comp.fileMeta(pos).SupportsArch(comp.target.Arch) {
			return true
		}
		switch n.(type) {
		case *ast.Resource, *ast.Struct, *ast.Call, *ast.TypeDef:
			// This is required to keep the unsupported diagnostic working,
			// otherwise sysgen will think that these things are still supported on some arches.
			comp.unsupported[typ+" "+name] = true
		}
		return false
	})
}

func (comp *compiler) structIsVarlen(name string) bool {
	if varlen, ok := comp.structVarlen[name]; ok {
		return varlen
	}
	s := comp.structs[name]
	if s.IsUnion {
		res := comp.parseIntAttrs(unionAttrs, s, s.Attrs)
		if res[attrVarlen] != 0 {
			comp.structVarlen[name] = true
			return true
		}
	}
	comp.structVarlen[name] = false // to not hang on recursive types
	varlen := false
	for _, fld := range s.Fields {
		hasIfAttr := false
		for _, attr := range fld.Attrs {
			if structFieldAttrs[attr.Ident] == attrIf {
				hasIfAttr = true
			}
		}
		if hasIfAttr || comp.isVarlen(fld.Type) {
			varlen = true
			break
		}
	}
	comp.structVarlen[name] = varlen
	return varlen
}

func (comp *compiler) parseIntAttrs(descs map[string]*attrDesc, parent ast.Node,
	attrs []*ast.Type) map[*attrDesc]uint64 {
	intAttrs, _, _ := comp.parseAttrs(descs, parent, attrs)
	return intAttrs
}

func (comp *compiler) parseAttrs(descs map[string]*attrDesc, parent ast.Node, attrs []*ast.Type) (
	map[*attrDesc]uint64, map[*attrDesc]prog.Expression, map[*attrDesc]string) {
	_, parentType, parentName := parent.Info()
	resInt := make(map[*attrDesc]uint64)
	resExpr := make(map[*attrDesc]prog.Expression)
	resString := make(map[*attrDesc]string)
	for _, attr := range attrs {
		if unexpected, _, ok := checkTypeKind(attr, kindIdent); !ok {
			comp.error(attr.Pos, "unexpected %v, expect attribute", unexpected)
			return resInt, resExpr, resString
		}
		if len(attr.Colon) != 0 {
			comp.error(attr.Colon[0].Pos, "unexpected ':'")
			return resInt, resExpr, resString
		}
		desc := descs[attr.Ident]
		if desc == nil {
			comp.error(attr.Pos, "unknown %v %v attribute %v", parentType, parentName, attr.Ident)
			return resInt, resExpr, resString
		}
		_, dupInt := resInt[desc]
		_, dupExpr := resExpr[desc]
		_, dupString := resString[desc]
		if dupInt || dupExpr || dupString {
			comp.error(attr.Pos, "duplicate %v %v attribute %v", parentType, parentName, attr.Ident)
			return resInt, resExpr, resString
		}

		switch desc.Type {
		case flagAttr:
			resInt[desc] = 1
			if len(attr.Args) != 0 {
				comp.error(attr.Pos, "%v attribute has args", attr.Ident)
				return nil, nil, nil
			}
		case intAttr:
			resInt[desc] = comp.parseAttrIntArg(attr)
		case exprAttr:
			resExpr[desc] = comp.parseAttrExprArg(attr)
		case stringAttr:
			resString[desc] = comp.parseAttrStringArg(attr)
		default:
			comp.error(attr.Pos, "attribute %v has unknown type", attr.Ident)
			return nil, nil, nil
		}
	}
	return resInt, resExpr, resString
}

func (comp *compiler) parseAttrExprArg(attr *ast.Type) prog.Expression {
	if len(attr.Args) != 1 {
		comp.error(attr.Pos, "%v attribute is expected to have only one argument", attr.Ident)
		return nil
	}
	arg := attr.Args[0]
	if arg.HasString {
		comp.error(attr.Pos, "%v argument must be an expression", attr.Ident)
		return nil
	}
	return comp.genExpression(arg)
}

func (comp *compiler) parseAttrIntArg(attr *ast.Type) uint64 {
	if len(attr.Args) != 1 {
		comp.error(attr.Pos, "%v attribute is expected to have 1 argument", attr.Ident)
		return 0
	}
	sz := attr.Args[0]
	if unexpected, _, ok := checkTypeKind(sz, kindInt); !ok {
		comp.error(sz.Pos, "unexpected %v, expect int", unexpected)
		return 0
	}
	if len(sz.Colon) != 0 || len(sz.Args) != 0 {
		comp.error(sz.Pos, "%v attribute has colon or args", attr.Ident)
		return 0
	}
	return sz.Value
}

func (comp *compiler) parseAttrStringArg(attr *ast.Type) string {
	if len(attr.Args) != 1 {
		comp.error(attr.Pos, "%v attribute is expected to have 1 argument", attr.Ident)
		return ""
	}
	arg := attr.Args[0]
	if !arg.HasString {
		comp.error(attr.Pos, "%v argument must be a string", attr.Ident)
		return ""
	}
	return arg.String
}

func (comp *compiler) getTypeDesc(t *ast.Type) *typeDesc {
	if desc := builtinTypes[t.Ident]; desc != nil {
		return desc
	}
	if comp.resources[t.Ident] != nil {
		return typeResource
	}
	if comp.structs[t.Ident] != nil {
		return typeStruct
	}
	if comp.typedefs[t.Ident] != nil {
		return typeTypedef
	}
	return nil
}

func (comp *compiler) getArgsBase(t *ast.Type, isArg bool) (*typeDesc, []*ast.Type, prog.IntTypeCommon) {
	desc := comp.getTypeDesc(t)
	if desc == nil {
		panic(fmt.Sprintf("no type desc for %#v", *t))
	}
	args, opt := removeOpt(t)
	com := genCommon(t.Ident, sizeUnassigned, opt != nil)
	base := genIntCommon(com, 0, false)
	if desc.NeedBase {
		base.TypeSize = comp.ptrSize
		if !isArg {
			baseType := args[len(args)-1]
			args = args[:len(args)-1]
			base = typeInt.Gen(comp, baseType, nil, base).(*prog.IntType).IntTypeCommon
		}
	}
	return desc, args, base
}

func (comp *compiler) derefPointers(t *ast.Type) (*ast.Type, *typeDesc) {
	for {
		desc := comp.getTypeDesc(t)
		if desc != typePtr {
			return t, desc
		}
		t = t.Args[1]
	}
}

func (comp *compiler) foreachType(n0 ast.Node,
	cb func(*ast.Type, *typeDesc, []*ast.Type, prog.IntTypeCommon)) {
	switch n := n0.(type) {
	case *ast.Call:
		for _, arg := range n.Args {
			comp.foreachSubType(arg.Type, true, cb)
		}
		if n.Ret != nil {
			comp.foreachSubType(n.Ret, true, cb)
		}
	case *ast.Resource:
		comp.foreachSubType(n.Base, false, cb)
	case *ast.Struct:
		for _, f := range n.Fields {
			comp.foreachSubType(f.Type, false, cb)
		}
	case *ast.TypeDef:
		if len(n.Args) == 0 {
			comp.foreachSubType(n.Type, false, cb)
		}
	default:
		panic(fmt.Sprintf("unexpected node %#v", n0))
	}
}

func (comp *compiler) foreachSubType(t *ast.Type, isArg bool,
	cb func(*ast.Type, *typeDesc, []*ast.Type, prog.IntTypeCommon)) {
	desc, args, base := comp.getArgsBase(t, isArg)
	cb(t, desc, args, base)
	for i, arg := range args {
		if desc.Args[i].Type == typeArgType {
			comp.foreachSubType(arg, desc.Args[i].IsArg, cb)
		}
	}
}

func removeOpt(t *ast.Type) ([]*ast.Type, *ast.Type) {
	args := t.Args
	if last := len(args) - 1; last >= 0 && args[last].Ident == "opt" {
		return args[:last], args[last]
	}
	return args, nil
}

func (comp *compiler) parseIntType(name string) (size uint64, bigEndian bool) {
	be := strings.HasSuffix(name, "be")
	if be {
		name = name[:len(name)-len("be")]
	}
	size = comp.ptrSize
	if name != "intptr" {
		size, _ = strconv.ParseUint(name[3:], 10, 64)
		size /= 8
	}
	return size, be
}

func arrayContains(a []string, v string) bool {
	for _, s := range a {
		if s == v {
			return true
		}
	}
	return false
}

func (comp *compiler) flattenFlags() {
	comp.flattenIntFlags()
	comp.flattenStrFlags()

	for _, n := range comp.desc.Nodes {
		switch flags := n.(type) {
		case *ast.IntFlags:
			// It's possible that we don't find the flag in intFlags if it was
			// skipped due to errors (or special name "_") when populating
			// intFlags (see checkNames).
			if f, ok := comp.intFlags[flags.Name.Name]; ok {
				flags.Values = f.Values
			}
		case *ast.StrFlags:
			// Same as for intFlags above.
			if f, ok := comp.strFlags[flags.Name.Name]; ok {
				flags.Values = f.Values
			}
		}
	}
}

func (comp *compiler) flattenIntFlags() {
	for name, flags := range comp.intFlags {
		if err := recurFlattenFlags[*ast.IntFlags, *ast.Int](comp, name, flags, comp.intFlags,
			map[string]bool{}); err != nil {
			comp.error(flags.Pos, "%v", err)
		}
	}
}

func (comp *compiler) flattenStrFlags() {
	for name, flags := range comp.strFlags {
		if err := recurFlattenFlags[*ast.StrFlags, *ast.String](comp, name, flags, comp.strFlags,
			map[string]bool{}); err != nil {
			comp.error(flags.Pos, "%v", err)
		}
	}
}

func recurFlattenFlags[F ast.Flags[V], V ast.FlagValue](comp *compiler, name string, flags F,
	allFlags map[string]F, visitedFlags map[string]bool) error {
	if _, visited := visitedFlags[name]; visited {
		return fmt.Errorf("flags %v used twice or circular dependency on %v", name, name)
	}
	visitedFlags[name] = true

	var values []V
	for _, flag := range flags.GetValues() {
		if f, isFlags := allFlags[flag.GetName()]; isFlags {
			if err := recurFlattenFlags[F, V](comp, flag.GetName(), f, allFlags, visitedFlags); err != nil {
				return err
			}
			values = append(values, allFlags[flag.GetName()].GetValues()...)
		} else {
			values = append(values, flag)
		}
	}
	if len(values) > 100000 {
		return fmt.Errorf("%v has more than 100000 values %v", name, len(values))
	}
	flags.SetValues(values)
	return nil
}
