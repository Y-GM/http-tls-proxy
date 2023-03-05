package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/Kolosok86/http"
	"github.com/Kolosok86/http/httputil"
	"github.com/kolosok86/proxy/internal/core"
)

const BAD_REQ_MSG = "Bad Request\n"

type ProxyHandler struct {
	timeout   time.Duration
	logger    *core.Logger
	transport http.RoundTripper
}

func NewProxyHandler(timeout time.Duration, logger *core.Logger) *ProxyHandler {
	return &ProxyHandler{
		transport: &http.Transport{},
		timeout:   timeout,
		logger:    logger,
	}
}

func (s *ProxyHandler) ServeHTTP(wr http.ResponseWriter, req *http.Request) {
	isConnect := strings.ToUpper(req.Method) == "CONNECT"
	if (req.URL.Host == "" || req.URL.Scheme == "" && !isConnect) && req.ProtoMajor < 2 ||
		req.Host == "" && req.ProtoMajor == 2 {
		http.Error(wr, BAD_REQ_MSG, http.StatusBadRequest)
		return
	}

	s.logger.Info("Request: %v %v %v %v", req.RemoteAddr, req.Proto, req.Method, req.URL)

	if !isConnect {
		http.Error(wr, BAD_REQ_MSG, http.StatusBadRequest)
	} else {
		s.HandleTunnel(wr, req)
	}
}

func (s *ProxyHandler) HandleTunnel(wr http.ResponseWriter, req *http.Request) {
	if req.ProtoMajor == 2 {
		s.logger.Error("Unsupported protocol version: %s", req.Proto)
		http.Error(wr, "Unsupported protocol version.", http.StatusBadRequest)
		return
	}

	// Upgrade client connection
	local, reader, err := core.Hijack(wr)
	if err != nil {
		s.logger.Error("Can't hijack client connection: %v", err)
		http.Error(wr, "Can't hijack client connection", http.StatusInternalServerError)
		return
	}

	defer local.Close()

	// Inform client connection is built
	fmt.Fprintf(local, "HTTP/%d.%d 200 OK\r\n\r\n", req.ProtoMajor, req.ProtoMinor)

	request, err := core.ReadRequest(reader.Reader, "http")
	if err != nil {
		s.logger.Error("HTTP read error: %v", err)
		http.Error(wr, "Server Read Error", http.StatusInternalServerError)
		return
	}

	scheme := request.Header.Get("proxy-protocol")
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}

	hash := request.Header.Get("proxy-tls")
	request.URL.Scheme = scheme

	client := &http.Client{
		Transport: core.NewRoundTripper(hash, "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:87.0) Gecko/20100101 Firefox/87.0"),
		Timeout:   10 * time.Second,
	}

	core.RemoveServiceHeaders(request)

	resp, err := client.Do(request)
	if err != nil {
		s.logger.Error("HTTP fetch error: %v", err)
		http.Error(wr, "Server Request Error", http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()

	s.logger.Info("%v %v %v %v", req.RemoteAddr, req.Method, req.URL, resp.Status)

	raw, err := httputil.DumpResponse(resp, true)
	if err != nil {
		s.logger.Error("HTTP dump error: %v", err)
		http.Error(wr, "Server Dump Error", http.StatusInternalServerError)
		return
	}

	_, err = fmt.Fprintf(local, "%s", raw)
	if err != nil {
		s.logger.Error("HTTP dump error: %v", err)
		http.Error(wr, "Server Send Response Error", http.StatusInternalServerError)
	}
}
