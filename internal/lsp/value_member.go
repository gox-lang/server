package lsp

// typedValueMember resolves a field or method selected from a local value using
// the same type-member index that powers member completion. Keeping this lookup
// shared prevents completion, hover, and signature help from disagreeing about
// members of inferred or explicitly typed locals.
func (s *Server) typedValueMember(document Document, qualifier, member string, offset int) (CompletionItem, bool) {
	local, ok := s.symbols.LocalSymbolAt(document.URI, qualifier, offset)
	if !ok {
		return CompletionItem{}, false
	}
	for _, item := range s.typeMemberCompletions(document, local.Type, member) {
		if item.Label == member {
			return item, true
		}
	}
	return CompletionItem{}, false
}
