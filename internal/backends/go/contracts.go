package gobackend

import (
	"fmt"
	"go/types"
	"sort"

	"github.com/gtindo/flowmap/internal/semantic"
)

// readableType preserves the established short Go package qualifiers.
func readableType(value types.Type) string {
	return types.TypeString(value, func(pkg *types.Package) string { return pkg.Name() })
}

func tupleStrings(tuple *types.Tuple, variadic bool) []string {
	values := make([]string, 0, tuple.Len())
	for index := 0; index < tuple.Len(); index++ {
		item := tuple.At(index)
		typeName := readableType(item.Type())
		if variadic && index == tuple.Len()-1 {
			if slice, ok := item.Type().(*types.Slice); ok {
				typeName = "..." + readableType(slice.Elem())
			}
		}
		if item.Name() == "" {
			values = append(values, typeName)
			continue
		}
		values = append(values, fmt.Sprintf("%s %s", item.Name(), typeName))
	}
	return values
}

func signatureContracts(signature *types.Signature) []semantic.Contract {
	contracts := make(map[string]semantic.Contract)
	for _, tuple := range []*types.Tuple{signature.Params(), signature.Results()} {
		for index := 0; index < tuple.Len(); index++ {
			collectContract(tuple.At(index).Type(), contracts)
		}
	}

	names := make([]string, 0, len(contracts))
	for name := range contracts {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]semantic.Contract, 0, len(names))
	for _, name := range names {
		result = append(result, contracts[name])
	}
	return result
}

func collectContract(value types.Type, contracts map[string]semantic.Contract) {
	switch typed := value.(type) {
	case *types.Pointer:
		collectContract(typed.Elem(), contracts)
	case *types.Slice:
		collectContract(typed.Elem(), contracts)
	case *types.Array:
		collectContract(typed.Elem(), contracts)
	case *types.Map:
		collectContract(typed.Key(), contracts)
		collectContract(typed.Elem(), contracts)
	case *types.Named:
		object := typed.Obj()
		name := object.Name()
		if object.Pkg() != nil {
			name = object.Pkg().Name() + "." + name
		}
		if _, exists := contracts[name]; exists {
			return
		}
		switch underlying := typed.Underlying().(type) {
		case *types.Struct:
			fields := make([]semantic.Field, 0, underlying.NumFields())
			for index := 0; index < underlying.NumFields(); index++ {
				field := underlying.Field(index)
				fields = append(fields, semantic.Field{Name: field.Name(), Type: readableType(field.Type())})
			}
			contracts[name] = semantic.Contract{Name: name, Kind: "struct", Fields: fields}
		case *types.Interface:
			underlying.Complete()
			methods := make([]string, 0, underlying.NumMethods())
			for index := 0; index < underlying.NumMethods(); index++ {
				methods = append(methods, underlying.Method(index).String())
			}
			contracts[name] = semantic.Contract{Name: name, Kind: "interface", Methods: methods}
		}
	}
}
