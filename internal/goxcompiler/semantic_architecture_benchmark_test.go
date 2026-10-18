package gox

import "testing"

func BenchmarkSemanticArchitectureParseModuleGoOnly(b *testing.B) {
	source := `module page
import (
    "reflect"
    "strings"
)
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(text)
    _ = reflect.ValueOf(&text).Elem()
}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseModule("page.gox", source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSemanticArchitectureParseModuleTemplateValue(b *testing.B) {
	source := `module page
import "reflect"
func Build() {
    text := "hello"
    view := <span>{{reflect.ValueOf(&text).Elem()}}</span>
    _ = view
}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseModule("page.gox", source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSemanticArchitectureSemanticProjectionTemplate(b *testing.B) {
	source := `module page
import (
    "reflect"
    "strings"
)
func Build() interface{} {
    text := "hello"
    view := <span>{{strings.NewReplacer("a", "b").Replace(text)}}</span>
    _ = view
    return <main>{{reflect.ValueOf(&text).Elem()}}</main>
}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := BuildSemanticProjection("page.gox", source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSemanticArchitectureSemanticProjectionGoxMarkers(b *testing.B) {
	source := `module page
import (
    "reflect"
    "strings"
)
type Page interface{}
type Action interface{}
type Component interface{}
func Index(text string) Page {
    if text != "" {
        return <main>{{strings.NewReplacer("a", "b").Replace(text)}}</main>
    }
}
func Login(text string) Action {
    return reflect.ValueOf(&text).Elem().CanSet()
}
func Card(text string) Component {
    return <span>{{strings.TrimSpace(text)}}</span>
}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := BuildSemanticProjection("page.gox", source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSemanticArchitectureSemanticProjectionDirectives(b *testing.B) {
	source := `module page

type User struct { Name string }

func Build(users []User, count int, active bool, status string) interface{} {
	return <main>
		<:if-group>
			<:if condition={{ active }}>{{ status }}</:if>
			<:else>{{ count }}</:else>
		</:if-group>
		<:for index user range={{ users }}>
			<:empty>{{ count }}</:empty>
			<:each>{{ index }} {{ user.Name }}</:each>
		</:for>
		<:repeat attempt times={{ count }}>{{ attempt }}</:repeat>
		<:switch value={{ status }}>
			<:case is={{ "ready", "cached" }} fallthrough>{{ status }}</:case>
			<:default>{{ status }}</:default>
		</:switch>
	</main>
}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := BuildSemanticProjection("page.gox", source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSemanticArchitectureParseModuleDirectives(b *testing.B) {
	source := `module page

type User struct { Name string }

func Build(users []User, count int, active bool, status string) interface{} {
	return <main>
		<:if condition={{ active }}>{{ status }}</:if>
		<:for index user range={{ users }}>
			<:empty>{{ count }}</:empty>
			<:each>{{ index }} {{ user.Name }}</:each>
		</:for>
		<:repeat attempt times={{ count }}>{{ attempt }}</:repeat>
		<:switch value={{ status }}>
			<:case is={{ "ready", "cached" }} fallthrough>{{ status }}</:case>
			<:default>{{ status }}</:default>
		</:switch>
	</main>
}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseModule("page.gox", source); err != nil {
			b.Fatal(err)
		}
	}
}
