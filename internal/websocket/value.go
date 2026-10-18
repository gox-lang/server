package websocket

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"time"

	"github.com/gox-lang/gox"
)

const maxJavaScriptSafeInteger int64 = 1<<53 - 1

var (
	timeType      = reflect.TypeOf(time.Time{})
	bigIntType    = reflect.TypeOf(big.Int{})
	byteSliceType = reflect.TypeOf([]byte(nil))
	fileType      = reflect.TypeOf(gox.File{})
	filesType     = reflect.TypeOf([]gox.File(nil))
)

type Transfer struct {
	ID       uint64
	Name     string
	MIMEType string
	Size     int64
	File     gox.File
}

type BindingError struct {
	Code     string
	Argument int
	Received string
	Expected string
	Cause    error
}

func (err *BindingError) Error() string {
	if err.Code == "ARGUMENT_COUNT_MISMATCH" {
		return err.Cause.Error()
	}
	return fmt.Sprintf("argument %d expected %s, received %s: %v", err.Argument, err.Expected, err.Received, err.Cause)
}

func (err *BindingError) Unwrap() error { return err.Cause }

// EncodeValue converts one server value into its stable wire category.
func EncodeValue(value any, clients ...gox.ClientIslandRuntime) (Argument, error) {
	if value == nil {
		return Argument{Type: ArgumentNull}, nil
	}
	if date, ok := value.(time.Time); ok {
		return Argument{Type: ArgumentDate, Data: []byte(date.Format(time.RFC3339Nano))}, nil
	}
	if date, ok := value.(*time.Time); ok {
		if date == nil {
			return Argument{Type: ArgumentNull}, nil
		}
		return Argument{Type: ArgumentDate, Data: []byte(date.Format(time.RFC3339Nano))}, nil
	}
	if htmlValue, ok := value.(gox.HTML); ok {
		return Argument{Type: ArgumentHTML, Data: []byte(htmlValue)}, nil
	}
	if render, ok := value.(func(*gox.Context)); ok {
		data, err := renderHTML(func(writer gox.Writer) {
			ctx := &gox.Context{Writer: writer}
			if len(clients) > 0 {
				gox.BindClientIslandRuntime(ctx, clients[0])
			}
			render(ctx)
		})
		if err != nil {
			return Argument{}, err
		}
		return Argument{Type: ArgumentHTML, Data: data}, nil
	}
	if render, ok := value.(func(gox.Writer)); ok {
		data, err := renderHTML(render)
		if err != nil {
			return Argument{}, err
		}
		return Argument{Type: ArgumentHTML, Data: data}, nil
	}
	switch integer := value.(type) {
	case big.Int:
		data, _ := EncodeBigInt(&integer)
		return Argument{Type: ArgumentBigInt, Data: data}, nil
	case *big.Int:
		if integer == nil {
			return Argument{Type: ArgumentNull}, nil
		}
		data, err := EncodeBigInt(integer)
		return Argument{Type: ArgumentBigInt, Data: data}, err
	case []byte:
		return Argument{Type: ArgumentBinary, Data: append([]byte(nil), integer...)}, nil
	}

	rv := reflect.ValueOf(value)
	if isNilValue(rv) {
		return Argument{Type: ArgumentNull}, nil
	}
	switch rv.Kind() {
	case reflect.String:
		return Argument{Type: ArgumentText, Data: []byte(rv.String())}, nil
	case reflect.Bool:
		if rv.Bool() {
			return Argument{Type: ArgumentBoolean, Data: []byte{1}}, nil
		}
		return Argument{Type: ArgumentBoolean, Data: []byte{0}}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		integer := rv.Int()
		data := []byte(strconv.FormatInt(integer, 10))
		if integer < -maxJavaScriptSafeInteger || integer > maxJavaScriptSafeInteger {
			return Argument{Type: ArgumentBigInt, Data: data}, nil
		}
		return Argument{Type: ArgumentNumber, Data: data}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		integer := rv.Uint()
		data := []byte(strconv.FormatUint(integer, 10))
		if integer > uint64(maxJavaScriptSafeInteger) {
			return Argument{Type: ArgumentBigInt, Data: data}, nil
		}
		return Argument{Type: ArgumentNumber, Data: data}, nil
	case reflect.Float32, reflect.Float64:
		value := rv.Float()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Argument{}, fmt.Errorf("non-finite numbers cannot be sent")
		}
		return Argument{Type: ArgumentNumber, Data: []byte(strconv.FormatFloat(value, 'g', -1, rv.Type().Bits()))}, nil
	case reflect.Slice:
		if rv.Type().ConvertibleTo(byteSliceType) {
			data := rv.Convert(byteSliceType).Interface().([]byte)
			return Argument{Type: ArgumentBinary, Data: append([]byte(nil), data...)}, nil
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return Argument{}, fmt.Errorf("value of type %T cannot be encoded as JSON: %w", value, err)
	}
	return Argument{Type: ArgumentJSON, Data: data}, nil
}

func EncodeValues(values []any) ([]Argument, error) {
	arguments := make([]Argument, len(values))
	for index, value := range values {
		argument, err := EncodeValue(value)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", index, err)
		}
		if argument.Type == ArgumentFile || argument.Type == ArgumentFiles {
			return nil, fmt.Errorf("argument %d: file transfers are not integrated yet", index)
		}
		arguments[index] = argument
	}
	return arguments, nil
}

// EncodeValuesWithTransfers encodes event values and extracts file bodies into
// independent transfers referenced by FILE and FILES arguments.
func EncodeValuesWithTransfers(values []any, allocate func() uint64, clients ...gox.ClientIslandRuntime) ([]Argument, []Transfer, error) {
	if allocate == nil {
		return nil, nil, fmt.Errorf("transfer ID allocator cannot be nil")
	}
	arguments := make([]Argument, len(values))
	var transfers []Transfer
	for index, value := range values {
		if file, ok := value.(gox.File); ok {
			transfer, err := encodeFileTransfer(file, allocate())
			if err != nil {
				return nil, nil, fmt.Errorf("argument %d: %w", index, err)
			}
			data, err := EncodeFileReference(transfer.ID)
			if err != nil {
				return nil, nil, err
			}
			arguments[index] = Argument{Type: ArgumentFile, Data: data}
			transfers = append(transfers, transfer)
			continue
		}
		if files, ok := value.([]gox.File); ok {
			ids := make([]uint64, len(files))
			for fileIndex, file := range files {
				transfer, err := encodeFileTransfer(file, allocate())
				if err != nil {
					return nil, nil, fmt.Errorf("argument %d file %d: %w", index, fileIndex, err)
				}
				ids[fileIndex] = transfer.ID
				transfers = append(transfers, transfer)
			}
			data, err := EncodeFilesReference(ids)
			if err != nil {
				return nil, nil, err
			}
			arguments[index] = Argument{Type: ArgumentFiles, Data: data}
			continue
		}
		argument, err := EncodeValue(value, clients...)
		if err != nil {
			return nil, nil, fmt.Errorf("argument %d: %w", index, err)
		}
		arguments[index] = argument
	}
	return arguments, transfers, nil
}

func encodeFileTransfer(file gox.File, id uint64) (Transfer, error) {
	if id == 0 {
		return Transfer{}, fmt.Errorf("file transfer ID must be greater than zero")
	}
	if file.IsZero() {
		return Transfer{}, fmt.Errorf("file must have a name")
	}
	info := file.Info()
	if info.Name == "" {
		return Transfer{}, fmt.Errorf("file must have a name")
	}
	mimeType := info.ContentType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return Transfer{ID: id, Name: info.Name, MIMEType: mimeType, Size: info.Size, File: file}, nil
}

func BindArguments(arguments []Argument, expected []reflect.Type) ([]any, error) {
	return BindArgumentsWithTransfers(arguments, expected, nil)
}

func BindArgumentsWithTransfers(arguments []Argument, expected []reflect.Type, files map[uint64]gox.File) ([]any, error) {
	if len(arguments) != len(expected) {
		cause := fmt.Errorf("expected %d arguments, received %d", len(expected), len(arguments))
		return nil, &BindingError{Code: "ARGUMENT_COUNT_MISMATCH", Argument: -1, Received: strconv.Itoa(len(arguments)), Expected: strconv.Itoa(len(expected)), Cause: cause}
	}
	values := make([]any, len(arguments))
	for index, argument := range arguments {
		var value reflect.Value
		var err error
		if argument.Type == ArgumentFile || argument.Type == ArgumentFiles {
			value, err = bindFileArgument(argument, expected[index], files)
		} else {
			value, err = bindArgument(argument, expected[index])
		}
		if err != nil {
			return nil, &BindingError{Code: "ARGUMENT_TYPE_MISMATCH", Argument: index, Received: ArgumentTypeName(argument.Type), Expected: expected[index].String(), Cause: err}
		}
		values[index] = value.Interface()
	}
	return values, nil
}

func bindFileArgument(argument Argument, expected reflect.Type, files map[uint64]gox.File) (reflect.Value, error) {
	if argument.Type == ArgumentFile {
		if expected != fileType {
			return reflect.Value{}, typeMismatch(argument.Type, expected)
		}
		id, err := DecodeFileReference(argument.Data)
		if err != nil {
			return reflect.Value{}, err
		}
		file, ok := files[id]
		if !ok {
			return reflect.Value{}, fmt.Errorf("file transfer %d is unavailable", id)
		}
		return reflect.ValueOf(file), nil
	}
	if expected != filesType {
		return reflect.Value{}, typeMismatch(argument.Type, expected)
	}
	ids, err := DecodeFilesReference(argument.Data)
	if err != nil {
		return reflect.Value{}, err
	}
	resolved := make([]gox.File, len(ids))
	for index, id := range ids {
		file, ok := files[id]
		if !ok {
			return reflect.Value{}, fmt.Errorf("file transfer %d is unavailable", id)
		}
		resolved[index] = file
	}
	return reflect.ValueOf(resolved), nil
}

func ArgumentTypeName(argumentType ArgumentType) string {
	switch argumentType {
	case ArgumentNull:
		return "NULL"
	case ArgumentText:
		return "TEXT"
	case ArgumentNumber:
		return "NUMBER"
	case ArgumentBoolean:
		return "BOOLEAN"
	case ArgumentDate:
		return "DATE"
	case ArgumentJSON:
		return "JSON"
	case ArgumentBinary:
		return "BINARY"
	case ArgumentFile:
		return "FILE"
	case ArgumentFiles:
		return "FILES"
	case ArgumentBigInt:
		return "BIGINT"
	case ArgumentHTML:
		return "HTML"
	default:
		return fmt.Sprintf("0x%02x", byte(argumentType))
	}
}

func bindArgument(argument Argument, expected reflect.Type) (reflect.Value, error) {
	if argument.Type == ArgumentNull {
		if acceptsNil(expected) {
			return reflect.Zero(expected), nil
		}
		return reflect.Value{}, fmt.Errorf("null cannot bind to %s", expected)
	}
	if expected.Kind() == reflect.Interface {
		value, err := decodeUntyped(argument)
		if err != nil {
			return reflect.Value{}, err
		}
		rv := reflect.ValueOf(value)
		if rv.Type().AssignableTo(expected) || rv.Type().Implements(expected) {
			return rv, nil
		}
		return reflect.Value{}, typeMismatch(argument.Type, expected)
	}

	switch argument.Type {
	case ArgumentText:
		if expected.Kind() != reflect.String {
			return reflect.Value{}, typeMismatch(argument.Type, expected)
		}
		value := reflect.New(expected).Elem()
		value.SetString(string(argument.Data))
		return value, nil
	case ArgumentHTML:
		if expected != reflect.TypeOf(gox.HTML("")) {
			return reflect.Value{}, typeMismatch(argument.Type, expected)
		}
		return reflect.ValueOf(gox.HTML(string(argument.Data))), nil
	case ArgumentBoolean:
		if expected.Kind() != reflect.Bool {
			return reflect.Value{}, typeMismatch(argument.Type, expected)
		}
		value := reflect.New(expected).Elem()
		value.SetBool(argument.Data[0] == 1)
		return value, nil
	case ArgumentNumber:
		return bindNumber(string(argument.Data), expected)
	case ArgumentBigInt:
		return bindBigInt(argument.Data, expected)
	case ArgumentDate:
		date, err := time.Parse(time.RFC3339Nano, string(argument.Data))
		if err != nil {
			return reflect.Value{}, err
		}
		if expected == timeType {
			return reflect.ValueOf(date), nil
		}
		if expected.Kind() == reflect.Pointer && expected.Elem() == timeType {
			return reflect.ValueOf(&date), nil
		}
		return reflect.Value{}, typeMismatch(argument.Type, expected)
	case ArgumentBinary:
		if expected.Kind() != reflect.Slice || expected.Elem().Kind() != reflect.Uint8 {
			return reflect.Value{}, typeMismatch(argument.Type, expected)
		}
		value := reflect.MakeSlice(expected, len(argument.Data), len(argument.Data))
		for index, part := range argument.Data {
			value.Index(index).SetUint(uint64(part))
		}
		return value, nil
	case ArgumentJSON:
		value := reflect.New(expected)
		if err := json.Unmarshal(argument.Data, value.Interface()); err != nil {
			return reflect.Value{}, err
		}
		return value.Elem(), nil
	default:
		return reflect.Value{}, fmt.Errorf("wire type 0x%02x is not supported by the event binder", byte(argument.Type))
	}
}

func bindNumber(text string, expected reflect.Type) (reflect.Value, error) {
	value := reflect.New(expected).Elem()
	switch expected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(text, 10, expected.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		parsed, err := strconv.ParseUint(text, 10, expected.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(text, expected.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetFloat(parsed)
	default:
		return reflect.Value{}, typeMismatch(ArgumentNumber, expected)
	}
	return value, nil
}

func bindBigInt(data []byte, expected reflect.Type) (reflect.Value, error) {
	integer, err := DecodeBigInt(data)
	if err != nil {
		return reflect.Value{}, err
	}
	if expected == bigIntType {
		return reflect.ValueOf(*integer), nil
	}
	if expected.Kind() == reflect.Pointer && expected.Elem() == bigIntType {
		return reflect.ValueOf(integer), nil
	}
	value := reflect.New(expected).Elem()
	switch expected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !integer.IsInt64() {
			return reflect.Value{}, fmt.Errorf("big integer overflows %s", expected)
		}
		parsed := integer.Int64()
		if value.OverflowInt(parsed) {
			return reflect.Value{}, fmt.Errorf("big integer overflows %s", expected)
		}
		value.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if integer.Sign() < 0 || !integer.IsUint64() {
			return reflect.Value{}, fmt.Errorf("big integer overflows %s", expected)
		}
		parsed := integer.Uint64()
		if value.OverflowUint(parsed) {
			return reflect.Value{}, fmt.Errorf("big integer overflows %s", expected)
		}
		value.SetUint(parsed)
	default:
		return reflect.Value{}, typeMismatch(ArgumentBigInt, expected)
	}
	return value, nil
}

func decodeUntyped(argument Argument) (any, error) {
	switch argument.Type {
	case ArgumentText:
		return string(argument.Data), nil
	case ArgumentHTML:
		return gox.HTML(string(argument.Data)), nil
	case ArgumentBoolean:
		return argument.Data[0] == 1, nil
	case ArgumentNumber:
		return strconv.ParseFloat(string(argument.Data), 64)
	case ArgumentBigInt:
		return DecodeBigInt(argument.Data)
	case ArgumentDate:
		return time.Parse(time.RFC3339Nano, string(argument.Data))
	case ArgumentBinary:
		return append([]byte(nil), argument.Data...), nil
	case ArgumentJSON:
		var value any
		err := json.Unmarshal(argument.Data, &value)
		return value, err
	default:
		return nil, fmt.Errorf("unsupported wire type 0x%02x", byte(argument.Type))
	}
}

func typeMismatch(received ArgumentType, expected reflect.Type) error {
	return fmt.Errorf("wire type 0x%02x cannot bind to %s", byte(received), expected)
}

func acceptsNil(value reflect.Type) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}

func isNilValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func EventName(packet Packet) string {
	for _, header := range packet.Headers {
		if header.Type == HeaderEventName {
			return string(header.Value)
		}
	}
	return ""
}

func HeaderUint(packet Packet, headerType HeaderType) (uint64, bool) {
	for _, header := range packet.Headers {
		if header.Type != headerType {
			continue
		}
		value, consumed, err := DecodeCompactUint(header.Value)
		return value, err == nil && consumed == len(header.Value)
	}
	return 0, false
}

func HeaderText(packet Packet, headerType HeaderType) string {
	for _, header := range packet.Headers {
		if header.Type == headerType {
			return string(header.Value)
		}
	}
	return ""
}
