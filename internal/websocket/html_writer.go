package websocket

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/big"
	"sort"
	"strconv"
	"sync"

	"github.com/gox-lang/gox"
)

const maxPooledHTMLWriterCapacity = 1 << 20

var htmlWriterPool = sync.Pool{New: func() any { return &htmlWriter{} }}

func acquireHTMLWriter() *htmlWriter {
	writer := htmlWriterPool.Get().(*htmlWriter)
	writer.Reset()
	return writer
}

func releaseHTMLWriter(writer *htmlWriter) {
	if writer == nil {
		return
	}
	if cap(writer.Bytes()) > maxPooledHTMLWriterCapacity {
		return
	}
	writer.Reset()
	htmlWriterPool.Put(writer)
}

type htmlWriter struct{ bytes.Buffer }

var _ gox.Writer = (*htmlWriter)(nil)

func renderHTML(render func(gox.Writer)) (data []byte, err error) {
	writer := acquireHTMLWriter()
	defer releaseHTMLWriter(writer)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("HTML render panicked: %v", recovered)
			data = nil
		}
	}()
	render(writer)
	return append([]byte(nil), writer.Bytes()...), nil
}

func (writer *htmlWriter) WriteTextString(value string) {
	_, _ = writer.WriteString(html.EscapeString(value))
}
func (writer *htmlWriter) WriteRawString(value string) { _, _ = writer.WriteString(value) }
func (writer *htmlWriter) WriteTextBytes(value []byte) { writer.WriteTextString(string(value)) }
func (writer *htmlWriter) WriteRawBytes(value []byte)  { _, _ = writer.Write(value) }
func (writer *htmlWriter) WriteBytes(value []byte)     { _, _ = writer.Write(value) }
func (writer *htmlWriter) WriteByte(value byte) error  { return writer.Buffer.WriteByte(value) }
func (writer *htmlWriter) WriteStream(value io.Reader) {
	if value != nil {
		_, _ = io.Copy(writer, value)
	}
}
func (writer *htmlWriter) WriteError(value error) {
	if value != nil {
		writer.WriteTextString(value.Error())
	}
}

func (writer *htmlWriter) WriteTextBool(value bool) {
	_, _ = writer.WriteString(strconv.FormatBool(value))
}
func (writer *htmlWriter) WriteRawBool(value bool)        { writer.WriteTextBool(value) }
func (writer *htmlWriter) WriteTextInt(value int)         { writer.writeInt64(int64(value)) }
func (writer *htmlWriter) WriteRawInt(value int)          { writer.WriteTextInt(value) }
func (writer *htmlWriter) WriteTextInt8(value int8)       { writer.writeInt64(int64(value)) }
func (writer *htmlWriter) WriteRawInt8(value int8)        { writer.WriteTextInt8(value) }
func (writer *htmlWriter) WriteTextInt16(value int16)     { writer.writeInt64(int64(value)) }
func (writer *htmlWriter) WriteRawInt16(value int16)      { writer.WriteTextInt16(value) }
func (writer *htmlWriter) WriteTextInt32(value int32)     { writer.writeInt64(int64(value)) }
func (writer *htmlWriter) WriteRawInt32(value int32)      { writer.WriteTextInt32(value) }
func (writer *htmlWriter) WriteTextInt64(value int64)     { writer.writeInt64(value) }
func (writer *htmlWriter) WriteRawInt64(value int64)      { writer.WriteTextInt64(value) }
func (writer *htmlWriter) WriteTextUint(value uint)       { writer.writeUint64(uint64(value)) }
func (writer *htmlWriter) WriteRawUint(value uint)        { writer.WriteTextUint(value) }
func (writer *htmlWriter) WriteTextUint8(value uint8)     { writer.writeUint64(uint64(value)) }
func (writer *htmlWriter) WriteRawUint8(value uint8)      { writer.WriteTextUint8(value) }
func (writer *htmlWriter) WriteTextUint16(value uint16)   { writer.writeUint64(uint64(value)) }
func (writer *htmlWriter) WriteRawUint16(value uint16)    { writer.WriteTextUint16(value) }
func (writer *htmlWriter) WriteTextUint32(value uint32)   { writer.writeUint64(uint64(value)) }
func (writer *htmlWriter) WriteRawUint32(value uint32)    { writer.WriteTextUint32(value) }
func (writer *htmlWriter) WriteTextUint64(value uint64)   { writer.writeUint64(value) }
func (writer *htmlWriter) WriteRawUint64(value uint64)    { writer.WriteTextUint64(value) }
func (writer *htmlWriter) WriteTextUintptr(value uintptr) { writer.writeUint64(uint64(value)) }
func (writer *htmlWriter) WriteRawUintptr(value uintptr)  { writer.WriteTextUintptr(value) }
func (writer *htmlWriter) WriteTextFloat32(value float32) { writer.writeFloat(float64(value), 32) }
func (writer *htmlWriter) WriteRawFloat32(value float32)  { writer.WriteTextFloat32(value) }
func (writer *htmlWriter) WriteTextFloat64(value float64) { writer.writeFloat(value, 64) }
func (writer *htmlWriter) WriteRawFloat64(value float64)  { writer.WriteTextFloat64(value) }

func (writer *htmlWriter) WriteTextBigInt(value *big.Int) {
	if value != nil {
		writer.WriteRawString(value.String())
	}
}
func (writer *htmlWriter) WriteRawBigInt(value *big.Int)      { writer.WriteTextBigInt(value) }
func (writer *htmlWriter) WriteTextBigIntValue(value big.Int) { writer.WriteRawString(value.String()) }
func (writer *htmlWriter) WriteRawBigIntValue(value big.Int)  { writer.WriteTextBigIntValue(value) }
func (writer *htmlWriter) WriteTextBigFloat(value *big.Float) {
	if value != nil {
		writer.WriteRawString(value.Text('g', -1))
	}
}
func (writer *htmlWriter) WriteRawBigFloat(value *big.Float) { writer.WriteTextBigFloat(value) }
func (writer *htmlWriter) WriteTextBigFloatValue(value big.Float) {
	writer.WriteRawString(value.Text('g', -1))
}
func (writer *htmlWriter) WriteRawBigFloatValue(value big.Float) {
	writer.WriteTextBigFloatValue(value)
}
func (writer *htmlWriter) WriteTextBigRat(value *big.Rat) {
	if value != nil {
		writer.WriteRawString(value.RatString())
	}
}
func (writer *htmlWriter) WriteRawBigRat(value *big.Rat) { writer.WriteTextBigRat(value) }
func (writer *htmlWriter) WriteTextBigRatValue(value big.Rat) {
	writer.WriteRawString(value.RatString())
}
func (writer *htmlWriter) WriteRawBigRatValue(value big.Rat) { writer.WriteTextBigRatValue(value) }

func (writer *htmlWriter) WriteTextAny(value any) { writer.writeAny(value, true) }
func (writer *htmlWriter) WriteRawAny(value any)  { writer.writeAny(value, false) }

func (writer *htmlWriter) WriteSpreadAttrs(attrs map[string]string) {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writer.WriteRawString(" ")
		writer.WriteTextString(key)
		writer.WriteRawString(`="`)
		writer.WriteTextString(attrs[key])
		writer.WriteRawString(`"`)
	}
}

func (writer *htmlWriter) writeInt64(value int64) {
	_, _ = writer.WriteString(strconv.FormatInt(value, 10))
}
func (writer *htmlWriter) writeUint64(value uint64) {
	_, _ = writer.WriteString(strconv.FormatUint(value, 10))
}
func (writer *htmlWriter) writeFloat(value float64, bits int) {
	_, _ = writer.WriteString(strconv.FormatFloat(value, 'g', -1, bits))
}
func (writer *htmlWriter) writeAny(value any, escaped bool) {
	if value == nil {
		return
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	case fmt.Stringer:
		text = typed.String()
	case error:
		text = typed.Error()
	default:
		data, err := json.Marshal(value)
		if err != nil {
			text = fmt.Sprint(value)
		} else {
			text = string(data)
		}
	}
	if escaped {
		writer.WriteTextString(text)
	} else {
		writer.WriteRawString(text)
	}
}
