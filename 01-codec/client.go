// Call 表示一个 RPC 调用的状态和数据。
package geerpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"geerpc/codec"
	"io"
	"log"
	"net"
	"sync"
)

type Call struct {
	Seq           uint64      // 序列号，用于唯一标识一个调用
	ServiceMethod string      // 服务方法名，例如 "Service.Method"
	Args          interface{} // 调用方法的参数
	Reply         interface{} // 调用方法的回复
	Error         error       // 调用过程中遇到的错误
	Done          chan *Call  // 通知通道，用于通知调用完成
}

// done 方法用于处理 Call 的完成状态。
func (call *Call) done() {
	call.Done <- call
}

// Client 表示一个 RPC 客户端，用于与远程服务进行通信。
type Client struct {
	cc       codec.Codec      // 编码/解码器，用于处理网络通信
	opt      *Option          // 配置选项
	sending  sync.Mutex       // 互斥锁，用于同步发送操作
	header   codec.Header     // 请求头
	mu       sync.Mutex       // 互斥锁，用于同步其他操作
	seq      uint64           // 序列号，用于唯一标识每个请求
	pending  map[uint64]*Call // 待处理的调用，用于跟踪请求
	closing  bool             // 标记客户端是否正在关闭
	shutdown bool             // 标记客户端是否已关闭
}

// 实现 io.Closer 接口，用于关闭客户端
var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

// 关闭连接
func (client *Client) Close() error {
	// 在这里实现关闭客户端的逻辑
	client.mu.Lock()
	defer client.mu.Unlock()

	if client.closing {
		return ErrShutdown
	}
	client.closing = true

	return client.cc.Close()
}

func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.shutdown && !client.closing
}

// registerCall 用于注册一个新的调用请求，并分配一个唯一的序列号。
func (client *Client) registerCall(call *Call) (uint64, error) {
	// 加锁以确保同步
	client.mu.Lock()
	defer client.mu.Unlock()
	// 检查客户端是否正在关闭或已关闭
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}

	// 分配一个唯一的序列号
	call.Seq = client.seq
	// 将调用请求加入 pending 列表
	client.pending[call.Seq] = call
	// 增加序列号
	client.seq++
	return call.Seq, nil
}

// removeCall 用于从 pending 列表中移除一个调用请求，并返回该调用请求。
func (client *Client) removeCall(seq uint64) *Call {
	// 加锁以确保同步
	client.mu.Lock()
	defer client.mu.Unlock()

	// 从 pending 列表中获取调用请求
	call := client.pending[seq]

	// 从 pending 列表中删除调用请求
	delete(client.pending, seq)

	return call
}

// terminateCalls 用于终止所有待处理的调用请求，并设置错误。
func (client *Client) terminateCalls(err error) {
	// 加锁以确保发送操作同步
	client.sending.Lock()
	defer client.sending.Unlock()

	// 加锁以确保其他操作同步
	client.mu.Lock()
	defer client.mu.Unlock()

	// 标记客户端已关闭
	client.shutdown = true

	// 遍历所有待处理的调用请求
	for _, call := range client.pending {
		// 设置调用请求的错误
		call.Error = err
		// 通知调用请求已完成
		call.done()
	}
}

func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}

		call := client.removeCall(h.Seq)

		switch {
		case call == nil:
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body" + err.Error())
			}
			call.done()
		}
	}

	client.terminateCalls(err)
}

func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]

	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error", err)
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: option error: ", err)
		_ = conn.Close()
		return nil, err
	}

	return newClientCodec(f(conn), opt), nil
}

func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1,
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	go client.receive()
	return client
}

func parseOptions(opts ...*Option) (*Option, error) {
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	defer func() {
		if client == nil {
			_ = conn.Close()
		}
	}()

	return NewClient(conn, opt)
}

func (client *Client) send(call *Call) {
	client.sending.Lock()
	defer client.sending.Unlock()

	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq)

		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}

	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

func (client *Client) Call(serviceMethod string, args, reply interface{}) error {
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}
