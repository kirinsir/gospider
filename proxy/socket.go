package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"gitee.com/baixudong/gospider/tools"
)

func (obj *Client) sockes5Handle(ctx context.Context, client *ProxyConn) error {
	defer client.Close()
	var err error
	if err = obj.verifySocket(client); err != nil {
		return err
	}
	//获取serverAddr
	addr, err := obj.getSocketAddr(client)
	if err != nil {
		return err
	}
	//获取host
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if strings.HasPrefix(addr, "127.0.0.1") || strings.HasPrefix(addr, "localhost") {
		if port == obj.port {
			return errors.New("loop addr error")
		}
	}
	//获取代理
	proxyUrl, err := obj.dialer.GetProxy(ctx, nil)
	if err != nil {
		return err
	}
	//获取schema
	httpsBytes, err := client.reader.Peek(1)
	if err != nil {
		return err
	}
	client.option.schema = "http"
	if httpsBytes[0] == 22 {
		client.option.schema = "https"
		client.option.method = http.MethodConnect
	}
	netword := "tcp"
	proxyServer, err := obj.dialer.DialContextForProxy(ctx, netword, client.option.schema, addr, host, proxyUrl)
	if err != nil {
		return err
	}
	server := newProxyCon(ctx, proxyServer, bufio.NewReader(proxyServer), *client.option, false)
	client.option.port = port
	client.option.host = host

	server.option.port = port
	server.option.host = host
	defer server.Close()
	return obj.copyMain(ctx, client, server)
}

func (obj *Client) getSocketAddr(client *ProxyConn) (string, error) {
	buf := make([]byte, 4)
	addr := ""
	_, err := io.ReadFull(client.reader, buf) //读取版本号，CMD，RSV ，ATYP ，ADDR ，PORT
	if err != nil {
		return addr, fmt.Errorf("read header failed:%w", err)
	}
	ver, cmd, atyp := buf[0], buf[1], buf[3]
	if ver != 5 {
		return addr, fmt.Errorf("not supported ver:%v", ver)
	}
	if cmd != 1 {
		return addr, fmt.Errorf("not supported cmd:%v", ver)
	}
	switch atyp {
	case 1: //ipv4地址
		if _, err = io.ReadFull(client.reader, buf); err != nil {
			return addr, fmt.Errorf("read atyp failed:%w", err)
		}
		addr = net.IPv4(buf[0], buf[1], buf[2], buf[3]).String()
	case 3: //域名
		hostSize, err := client.reader.ReadByte() //域名的长度
		if err != nil {
			return addr, fmt.Errorf("read hostSize failed:%w", err)
		}
		host := make([]byte, hostSize)
		if _, err = io.ReadFull(client.reader, host); err != nil {
			return addr, fmt.Errorf("read host failed:%w", err)
		}
		addr = tools.BytesToString(host)
	case 4: //IPv6地址
		host := make([]byte, 16)
		if _, err = io.ReadFull(client.reader, host); err != nil {
			return addr, fmt.Errorf("read atyp failed:%w", err)
		}
		addr = net.IP(host).String()
	default:
		return addr, errors.New("invalid atyp")
	}
	if _, err = io.ReadFull(client.reader, buf[:2]); err != nil { //读取端口号
		return addr, fmt.Errorf("read port failed:%w", err)
	}
	_, err = client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return net.JoinHostPort(addr, strconv.Itoa(int(binary.BigEndian.Uint16(buf[:2])))), err
}
func (obj *Client) verifySocket(client *ProxyConn) error {
	ver, err := client.reader.ReadByte() //读取第一个字节判断是否是socks5协议
	if err != nil {
		return fmt.Errorf("read ver failed:%w", err)
	}
	if ver != 5 {
		return fmt.Errorf("not supported ver:%v", ver)
	}
	methodSize, err := client.reader.ReadByte() //读取第二个字节,method 的长度，支持认证的方法数量
	if err != nil {
		return fmt.Errorf("read methodSize failed:%w", err)
	}
	methods := make([]byte, methodSize)
	if _, err = io.ReadFull(client.reader, methods); err != nil { //读取method，支持认证的方法
		return fmt.Errorf("read method failed:%w", err)
	}
	if obj.basic != "" && !obj.whiteVerify(client) { //开始验证用户名密码
		if bytes.IndexByte(methods, 2) == -1 {
			return errors.New("不支持用户名密码验证")
		}
		_, err = client.Write([]byte{5, 2}) //告诉客户端要进行用户名密码验证
		if err != nil {
			return err
		}
		okVar, err := client.reader.ReadByte() //获取版本，通常为0x01
		if err != nil {
			return err
		}
		Len, err := client.reader.ReadByte() //获取用户名的长度
		if err != nil {
			return err
		}
		user := make([]byte, Len)
		if _, err = io.ReadFull(client.reader, user); err != nil {
			return err
		}
		if Len, err = client.reader.ReadByte(); err != nil { //获取密码的长度
			return err
		}
		pass := make([]byte, Len)
		if _, err = io.ReadFull(client.reader, pass); err != nil {
			return err
		}
		if tools.BytesToString(user) != obj.usr || tools.BytesToString(pass) != obj.pwd {
			client.Write([]byte{okVar, 0xff}) //用户名密码错误
			return errors.New("用户名密码错误")
		}
		_, err = client.Write([]byte{okVar, 0}) //协商成功
		return err
	}
	if _, err = client.Write([]byte{5, 0}); err != nil { //协商成功
		return err
	}
	return err
}
