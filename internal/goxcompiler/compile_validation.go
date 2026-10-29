package gox

// CompileForValidation emits the same Go bundle used by a runtime entry, but
// does not build or publish client assets. The caller must compile the output
// with Go to check its imported packages and must never load that plugin.
func CompileForValidation(entryPath, outPath string) error {
	graph, err := LoadGraph(entryPath)
	if err != nil {
		return err
	}
	if err := validateProjectGoGoroutineSafety(graph.RootDir); err != nil {
		return err
	}
	bundle, err := BundleModules(graph)
	if err != nil {
		return err
	}
	return WriteBundle(bundle, outPath)
}
