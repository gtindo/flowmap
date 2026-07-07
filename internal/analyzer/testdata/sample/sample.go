// Package sample exercises Flowmap analysis fixtures.
package sample

import (
	"context"
	"example.com/dependency"
	"os"
	"strings"
)

// Input carries source text through the sample pipeline.
type Input struct{ Text string }

// Output carries normalized text out of the sample pipeline.
type Output struct{ Text string }

// Store is an imperative persistence boundary.
type Store interface {
	Save(context.Context, Output) error
}

// Router registers variadic HTTP callbacks.
type Router interface {
	GET(string, ...func())
}

// CallbackOwner supplies a method value callback.
type CallbackOwner struct{}

// Handle is a method-value callback fixture.
func (CallbackOwner) Handle() {}

// Run coordinates normalization and persistence.
// Side Effect (Edge): persists through the supplied store.
func Run(ctx context.Context, store Store, input Input) (Output, error) {
	output := Normalize(input)
	if err := store.Save(ctx, output); err != nil {
		return Output{}, err
	}
	return output, nil
}

// Normalize removes surrounding whitespace from input.
// Operations (Pure): returns fresh state and performs no I/O.
func Normalize(input Input) Output { return Output{Text: strings.TrimSpace(input.Text)} }

// Load reads one input from a filesystem edge.
func Load(path string) (Input, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Input{}, err
	}
	return Input{Text: string(contents)}, nil
}

// AssemblyHook represents an implementation supplied outside Go.
func AssemblyHook()

// AssemblyPure represents an authored pure implementation supplied outside Go.
// Operations (Pure): returns without observable effects.
func AssemblyPure()

// CallDependency exercises a vendored dependency outside the local graph.
func CallDependency() { dependency.External() }

var serverErrors = make(chan error, 1)

// StartWorker starts an anonymous goroutine that reports the server result.
func StartWorker() {
	go func() {
		serverErrors <- startHTTPServer()
	}()
}

func startHTTPServer() error { return nil }

// HandleSomething is a direct callback fixture.
func HandleSomething() {}

// GenericCallback is an instantiated callback fixture.
func GenericCallback[T any]() {}

// RegisterCallback accepts one callback without invoking it.
func RegisterCallback(func()) {}

// RegisterCallbacks exercises direct, method, generic, variadic, and literal dependencies.
func RegisterCallbacks(routes Router, owner CallbackOwner) {
	routes.GET("/", HandleSomething)
	RegisterCallback(owner.Handle)
	routes.GET("/more", GenericCallback[string], func() {})
}

// SideEffectCallback is deliberately effectful but is only registered below.
func SideEffectCallback() { _, _ = os.ReadFile("callback") }

// RegisterSideEffectCallback must not inherit effects through a dependency edge.
func RegisterSideEffectCallback() { RegisterCallback(SideEffectCallback) }

// ReturnNamedCallback returns a named function dependency.
func ReturnNamedCallback() func() { return HandleSomething }

// ReturnMethodCallback returns a method-value dependency.
func ReturnMethodCallback(owner CallbackOwner) func() { return owner.Handle }

// ReturnGenericCallback returns an instantiated generic dependency.
func ReturnGenericCallback() func() { return GenericCallback[string] }

// ReturnWrappedCallback returns a parenthesized, converted dependency.
func ReturnWrappedCallback() func() { return (func())((HandleSomething)) }

// ReturnClosureCallback returns an anonymous function dependency.
func ReturnClosureCallback() func() { return func() {} }

// BuildCallback supplies a function value through a direct call.
func BuildCallback() func() { return HandleSomething }

// ReturnCalledCallback returns the result of calling another function.
func ReturnCalledCallback() func() { return BuildCallback() }

// ExecuteReturningCallback invokes a supplied function from a return expression.
func ExecuteReturningCallback(callback func() error) error { return callback() }

// ReturningCallbackTarget is supplied to ExecuteReturningCallback.
func ReturningCallbackTarget() error { return nil }

// CallExecuteReturningCallback supplies a concrete function to an invoked parameter.
func CallExecuteReturningCallback() error {
	return ExecuteReturningCallback(ReturningCallbackTarget)
}

// Repeat recursively repeats a string.
func Repeat(value string, count int) string {
	if count <= 0 {
		return ""
	}
	return value + Repeat(value, count-1)
}

// Map applies a typed transform without effects.
func Map[T any, R any](values []T, transform func(T) R) []R {
	result := make([]R, 0, len(values))
	for _, value := range values {
		result = append(result, transform(value))
	}
	return result
}
