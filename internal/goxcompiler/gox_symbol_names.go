package gox

func GoxModuleName(name string) string {
	return "GoxModule" + name
}

func GoxTypeName(module, name string) string {
	return "GoxType" + module + name
}

func GoxConstName(module, name string) string {
	return "GoxConst" + module + name
}

func GoxActionName(module string, name string) string {
	return "GoxAction" + module + name
}

func GoxModuleConstructorName(name string) string {
	return "New" + GoxModuleName(name)
}

func GoxActionParamsName(module string, action string) string {
	return "GoxActionParams" + module + action
}
