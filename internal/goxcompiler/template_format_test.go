package gox

import "testing"

func TestFormatTemplateIndentsTagsAttributesDirectivesAndExpressions(t *testing.T) {
	source := `<article class='card'><:if condition={{user.Active}}><h2>{{fmt.Sprintf("Hello, %s",user.Name)}}</h2><&Badge active label={{user.Name}} /></:if><!-- note --></article>`
	formatted, err := FormatTemplate(source, "\t")
	if err != nil {
		t.Fatal(err)
	}
	want := `<article class="card">
		<:if condition={{ user.Active }}>
			<h2>{{ fmt.Sprintf("Hello, %s", user.Name) }}</h2>
			<&Badge active label={{ user.Name }} />
		</:if>
		<!-- note -->
	</article>`
	if formatted != want {
		t.Fatalf("formatted template:\n%s\nwant:\n%s", formatted, want)
	}
	again, err := FormatTemplate(formatted, "\t")
	if err != nil || again != formatted {
		t.Fatalf("template formatting is not stable: %v\n%s", err, again)
	}
}

func TestFormatTemplateWrapsLongAttributes(t *testing.T) {
	source := `<&ProfileCard display-name={{ user.DisplayName("Administrator") }} description="A deliberately long profile description for the formatter" active />`
	formatted, err := FormatTemplate(source, "\t")
	if err != nil {
		t.Fatal(err)
	}
	want := `<&ProfileCard
		display-name={{ user.DisplayName("Administrator") }}
		description="A deliberately long profile description for the formatter"
		active
	/>`
	if formatted != want {
		t.Fatalf("wrapped attributes:\n%s\nwant:\n%s", formatted, want)
	}
}

func TestFormatTemplatePreservesInlineText(t *testing.T) {
	formatted, err := FormatTemplate(`<p>Hello, <strong>George</strong>!</p>`, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if formatted != `<p>Hello, <strong>George</strong>!</p>` {
		t.Fatalf("inline text changed: %q", formatted)
	}
}

func TestFormatTemplatePreservesDirectiveClosureAndFormatsExpressionLists(t *testing.T) {
	source := `<:switch value={{status}}><:case is={{1,2}}></:case><:default /></:switch>`
	formatted, err := FormatTemplate(source, "\t")
	if err != nil {
		t.Fatal(err)
	}
	want := `<:switch value={{ status }}>
		<:case is={{ 1, 2 }}>
		</:case>
		<:default />
	</:switch>`
	if formatted != want {
		t.Fatalf("directive formatting:\n%s\nwant:\n%s", formatted, want)
	}
}

func TestFormatTemplateEmbeddedStyleAndScriptIndentationIsStable(t *testing.T) {
	source := `<section>
	<style>
				.menu {
					border: 0 0 0 1px
				}
			
			</style>
	<script>
				if (a < b) {
					console.log(a)
				}
			</script>
</section>`

	formatted, err := FormatTemplate(source, "\t")
	if err != nil {
		t.Fatal(err)
	}
	want := `<section>
		<style>
			.menu {
				border: 0 0 0 1px
			}
		</style>
		<script>
			if (a < b) {
				console.log(a)
			}
		</script>
	</section>`
	if formatted != want {
		t.Fatalf("embedded code formatting:\n%s\nwant:\n%s", formatted, want)
	}

	again, err := FormatTemplate(formatted, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if again != formatted {
		t.Fatalf("embedded code formatting is not stable:\nfirst:\n%s\nsecond:\n%s", formatted, again)
	}
}
