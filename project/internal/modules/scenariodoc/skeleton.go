package scenariodoc

import _ "embed"

//go:embed skeleton.json
var skeletonData []byte

// Skeleton — каркас «вручную» (FR-SC-02): один предмет торга, три
// развилки, четыре финала и один скрытый факт, проходит проверку без
// правок. Возвращает копию встроенных байтов — вызывающий код может
// свободно менять результат, не задевая встроенные данные пакета.
func Skeleton() []byte {
	out := make([]byte, len(skeletonData))
	copy(out, skeletonData)
	return out
}
