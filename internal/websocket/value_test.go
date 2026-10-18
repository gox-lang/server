package websocket

import (
	"bytes"
	"io"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/gox-lang/gox"
)

func TestFileAndFilesValuesUseIndependentOrderedTransfers(t *testing.T) {
	avatar, err := gox.NewFile(gox.FileConfig{Name: "avatar.png", Type: "image/png", Content: bytes.NewReader([]byte{1, 2, 3})})
	if err != nil {
		t.Fatal(err)
	}
	resume, err := gox.NewFile(gox.FileConfig{Name: "resume.pdf", Type: "application/pdf", Content: bytes.NewReader([]byte{4, 5})})
	if err != nil {
		t.Fatal(err)
	}
	next := uint64(40)
	arguments, transfers, err := EncodeValuesWithTransfers([]any{avatar, []gox.File{avatar, resume}}, func() uint64 {
		next++
		return next
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 3 || transfers[0].ID != 41 || transfers[1].ID != 42 || transfers[2].ID != 43 {
		t.Fatalf("transfers = %#v", transfers)
	}
	if transfers[0].Name != "avatar.png" || transfers[2].MIMEType != "application/pdf" {
		t.Fatalf("metadata = %#v", transfers)
	}
	if !bytes.Equal(readTransferFile(t, transfers[0]), []byte{1, 2, 3}) || !bytes.Equal(readTransferFile(t, transfers[2]), []byte{4, 5}) {
		t.Fatalf("transfer data = %#v", transfers)
	}
	fileID, err := DecodeFileReference(arguments[0].Data)
	if err != nil || fileID != 41 {
		t.Fatalf("FILE = %d, %v", fileID, err)
	}
	fileIDs, err := DecodeFilesReference(arguments[1].Data)
	if err != nil || len(fileIDs) != 2 || fileIDs[0] != 42 || fileIDs[1] != 43 {
		t.Fatalf("FILES = %v, %v", fileIDs, err)
	}

	resolved := map[uint64]gox.File{41: avatar, 42: avatar, 43: resume}
	values, err := BindArgumentsWithTransfers(arguments, []reflect.Type{reflect.TypeOf(gox.File{}), reflect.TypeOf([]gox.File{})}, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].(gox.File).Name() != "avatar.png" || values[1].([]gox.File)[1].Name() != "resume.pdf" {
		t.Fatalf("bound values = %#v", values)
	}
}

func TestFileTransferUsesMetadataSnapshot(t *testing.T) {
	file, err := gox.NewFile(gox.FileConfig{Name: "report.txt", Content: bytes.NewReader([]byte("ready"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := file.SetName("report.json"); err != nil {
		t.Fatal(err)
	}
	transfer, err := encodeFileTransfer(file, 7)
	if err != nil {
		t.Fatal(err)
	}
	if transfer.Name != "report.json" || transfer.MIMEType != "application/json" {
		t.Fatalf("metadata = %#v", transfer)
	}
	if data := readTransferFile(t, transfer); !bytes.Equal(data, []byte("ready")) {
		t.Fatalf("data = %q", data)
	}
}

func readTransferFile(t *testing.T, transfer Transfer) []byte {
	t.Helper()
	reader, err := transfer.File.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEncodeValuesUsesStableWireCategories(t *testing.T) {
	date := time.Date(2026, 7, 15, 12, 0, 0, 123, time.UTC)
	large := new(big.Int)
	large.SetString("123456789012345678901234567890", 10)
	arguments, err := EncodeValues([]any{
		nil,
		"Javier",
		42,
		true,
		date,
		map[string]any{"id": 7},
		[]byte{0, 255},
		large,
		uint64(1 << 53),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ArgumentType{
		ArgumentNull, ArgumentText, ArgumentNumber, ArgumentBoolean,
		ArgumentDate, ArgumentJSON, ArgumentBinary, ArgumentBigInt, ArgumentBigInt,
	}
	for index, argument := range arguments {
		if argument.Type != want[index] {
			t.Fatalf("argument %d type = 0x%02x, want 0x%02x", index, argument.Type, want[index])
		}
	}
}

func TestHTMLRendererUsesGoxWriterAndBindsAsHTML(t *testing.T) {
	argument, err := EncodeValue(func(writer gox.Writer) {
		writer.WriteRawString("<h1>")
		writer.WriteTextString("Javier & GOX")
		writer.WriteRawString("</h1>")
	})
	if err != nil {
		t.Fatal(err)
	}
	if argument.Type != ArgumentHTML || string(argument.Data) != "<h1>Javier &amp; GOX</h1>" {
		t.Fatalf("HTML argument = %#v", argument)
	}
	values, err := BindArguments([]Argument{argument}, []reflect.Type{reflect.TypeOf(gox.HTML(""))})
	if err != nil {
		t.Fatal(err)
	}
	if values[0].(gox.HTML) != gox.HTML("<h1>Javier &amp; GOX</h1>") {
		t.Fatalf("bound HTML = %#v", values[0])
	}
}

func TestHTMLRendererRecoversPanicsAndReturnsWriterToPool(t *testing.T) {
	if _, err := EncodeValue(func(gox.Writer) { panic("render failed") }); err == nil {
		t.Fatal("render panic was accepted")
	}
	argument, err := EncodeValue(func(writer gox.Writer) { writer.WriteRawString("<p>ready</p>") })
	if err != nil || string(argument.Data) != "<p>ready</p>" {
		t.Fatalf("next render = %#v, %v", argument, err)
	}
}

func TestBindArgumentsUsesGoHandlerSignature(t *testing.T) {
	type profile struct {
		ID int `json:"id"`
	}
	date := "2026-07-15T12:34:56.789Z"
	arguments := []Argument{
		{Type: ArgumentText, Data: []byte("Javier")},
		{Type: ArgumentNumber, Data: []byte("42")},
		{Type: ArgumentBoolean, Data: []byte{1}},
		{Type: ArgumentDate, Data: []byte(date)},
		{Type: ArgumentJSON, Data: []byte(`{"id":7}`)},
		{Type: ArgumentBinary, Data: []byte{0, 255}},
		{Type: ArgumentBigInt, Data: []byte("18446744073709551615")},
	}
	handler := func(string, int, bool, time.Time, profile, []byte, uint64) {}
	typeOf := reflect.TypeOf(handler)
	expected := make([]reflect.Type, typeOf.NumIn())
	for index := range expected {
		expected[index] = typeOf.In(index)
	}
	values, err := BindArguments(arguments, expected)
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != "Javier" || values[1] != 42 || values[2] != true {
		t.Fatalf("primitive values = %#v", values[:3])
	}
	if values[4].(profile).ID != 7 || !reflect.DeepEqual(values[5], []byte{0, 255}) || values[6] != uint64(18446744073709551615) {
		t.Fatalf("bound values = %#v", values)
	}
}

func TestBindArgumentsRejectsCountAndTypeMismatch(t *testing.T) {
	if _, err := BindArguments(nil, []reflect.Type{reflect.TypeOf(0)}); err == nil {
		t.Fatal("missing argument count was accepted")
	}
	if _, err := BindArguments([]Argument{{Type: ArgumentText, Data: []byte("42")}}, []reflect.Type{reflect.TypeOf(0)}); err == nil {
		t.Fatal("TEXT was bound to int")
	}
	if _, err := BindArguments([]Argument{{Type: ArgumentNumber, Data: []byte("256")}}, []reflect.Type{reflect.TypeOf(int8(0))}); err == nil {
		t.Fatal("overflowing NUMBER was accepted")
	}
}
