package gox

func appendImportIfMissing(imports []ImportDecl, imp ImportDecl) []ImportDecl {
	for _, existing := range imports {
		if existing.Path == imp.Path && existing.Alias == imp.Alias {
			return imports
		}
	}

	return append(imports, imp)
}
