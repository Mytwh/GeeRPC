package codec

/*
消息编解码相关的代码都在codec中
*/

import "io"

/*
serviceMethod 是服务名和方法名，通常是以 "Service.Method" 的格式表示的。
seq 是序列号，用于区分不同的请求和响应。
error 是错误信息，如果有的话。
*/
type Header struct {
	ServiceMethod string // format "Service.Method"
	Seq           uint64 // sequence number chosen by the client
	Error         string // error info, non-empty if any error occurred during RPC
}

// codec是对消息体编解码的接口

/*
io.Closer：
这个接口来自标准库的 io 包。它包含一个方法 Close() error，用于关闭资源，比如文件或网络连接等。通过嵌入 io.Closer 接口，Codec 接口自动继承了 Close 方法。
ReadHeader(*Header) error：
这是一个方法，它接受一个指向 Header 结构体的指针作为参数，并返回一个错误（如果有的话）。这个方法通常用于读取并解析消息或请求的头部信息。
ReadBody(interface{}) error：
这是另一个方法，它接受一个空接口（interface{}）作为参数，并返回一个错误（如果有的话）。这个方法用于读取消息或请求的主体内容，并将其反序列化到提供的参数对象中。
Write(*Header, interface{}) error：
这个方法接受两个参数：一个指向 Header 结构体的指针和一个空接口（interface{}）。它返回一个错误（如果有的话）。该方法用于将数据（包括头部和主体）序列化然后写出。
*/

type Codec interface {
	io.Closer
	ReadHeader(*Header) error
	ReadBody(interface{}) error
	Write(*Header, interface{}) error
}

// codec的构造函数

/*
type用来定义一种新的类型
NewCodecFunc 是一个函数类型，它接受一个 io.ReadWriteCloser 类型的参数，并返回一个 Codec接口类型的值。
Type 是一个字符串类型，用于表示编解码器的类型。
*/

type NewCodecFunc func(io.ReadWriteCloser) Codec

type Type string

const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json" // TODO: add other codec types
)

var NewCodecFuncMap map[Type]NewCodecFunc

func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc)
	NewCodecFuncMap[GobType] = NewGobCodec
}
