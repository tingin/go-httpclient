// Copyright 2014-2015 Liu Dong <ddliuhb@gmail.com>.
// Licensed under the MIT license.

// Powerful and easy to use http client
package httpclient

import (
	"fmt"

	"bytes"
	"strings"

	"time"

	"io"
	"io/ioutil"
	"sync"

	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"

	"compress/gzip"

	"mime/multipart"
)

// Constants definations
// CURL options, see https://github.com/bagder/curl/blob/169fedbdce93ecf14befb6e0e1ce6a2d480252a3/packages/OS400/curl.inc.in
const (
	VERSION   = "0.5.0"
	USERAGENT = "go-httpclient v" + VERSION

	PROXY_HTTP    = 0
	PROXY_SOCKS4  = 4
	PROXY_SOCKS5  = 5
	PROXY_SOCKS4A = 6

	// CURL like OPT
	OPT_AUTOREFERER       = 58
	OPT_FOLLOWLOCATION    = 52
	OPT_CONNECTTIMEOUT    = 78
	OPT_CONNECTTIMEOUT_MS = 156
	OPT_MAXREDIRS         = 68
	OPT_PROXYTYPE         = 101
	OPT_TIMEOUT           = 13
	OPT_TIMEOUT_MS        = 155
	OPT_COOKIEJAR         = 10082
	OPT_INTERFACE         = 10062
	OPT_PROXY             = 10004
	OPT_REFERER           = 10016
	OPT_USERAGENT         = 10018

	// Other OPT
	OPT_REDIRECT_POLICY = 100000
	OPT_PROXY_FUNC      = 100001
)

// String map of options
var CONST = map[string]int{
	"OPT_AUTOREFERER":       58,
	"OPT_FOLLOWLOCATION":    52,
	"OPT_CONNECTTIMEOUT":    78,
	"OPT_CONNECTTIMEOUT_MS": 156,
	"OPT_MAXREDIRS":         68,
	"OPT_PROXYTYPE":         101,
	"OPT_TIMEOUT":           13,
	"OPT_TIMEOUT_MS":        155,
	"OPT_COOKIEJAR":         10082,
	"OPT_INTERFACE":         10062,
	"OPT_PROXY":             10004,
	"OPT_REFERER":           10016,
	"OPT_USERAGENT":         10018,

	"OPT_REDIRECT_POLICY": 100000,
	"OPT_PROXY_FUNC":      100001,
}

// Default options for any clients.
var defaultOptions = map[int]interface{}{
	OPT_FOLLOWLOCATION: true,
	OPT_MAXREDIRS:      10,
	OPT_AUTOREFERER:    true,
	OPT_USERAGENT:      USERAGENT,
	OPT_COOKIEJAR:      true,
}

// These options affect transport, transport may not be reused if you change any
// of these options during a request.
var transportOptions = []int{
	OPT_CONNECTTIMEOUT,
	OPT_CONNECTTIMEOUT_MS,
	OPT_PROXYTYPE,
	OPT_TIMEOUT,
	OPT_TIMEOUT_MS,
	OPT_INTERFACE,
	OPT_PROXY,
	OPT_PROXY_FUNC,
}

// These options affect cookie jar, jar may not be reused if you change any of
// these options during a request.
var jarOptions = []int{
	OPT_COOKIEJAR,
}

// Thin wrapper of http.Response(can also be used as http.Response).
type Response struct {
	*http.Response
}

// Read response body into a byte slice.
func (this *Response) ReadAll() ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch this.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(this.Body)
		if err != nil {
			return nil, err
		}
	default:
		reader = this.Body
	}

	defer reader.Close()
	return ioutil.ReadAll(reader)
}

// Read response body into string.
func (this *Response) ToString() (string, error) {
	bytes, err := this.ReadAll()
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

// Prepare a request.
func prepareRequest(method string, url_ string, headers map[string]string,
	body io.Reader, options map[int]interface{}) (*http.Request, error) {
	req, err := http.NewRequest(method, url_, body)

	if err != nil {
		return nil, err
	}

	// OPT_REFERER
	if referer, ok := options[OPT_REFERER]; ok {
		if refererStr, ok := referer.(string); ok {
			req.Header.Set("Referer", refererStr)
		}
	}

	// OPT_USERAGENT
	if useragent, ok := options[OPT_USERAGENT]; ok {
		if useragentStr, ok := useragent.(string); ok {
			req.Header.Set("User-Agent", useragentStr)
		}
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return req, nil
}

// Prepare a transport.
//
// Handles timemout, proxy and maybe other transport related options here.
func prepareTransport(options map[int]interface{}) (http.RoundTripper, error) {
	transport := &http.Transport{}

	connectTimeoutMS := 0

	if connectTimeoutMS_, ok := options[OPT_CONNECTTIMEOUT_MS]; ok {
		if connectTimeoutMS, ok = connectTimeoutMS_.(int); !ok {
			return nil, fmt.Errorf("OPT_CONNECTTIMEOUT_MS must be int")
		}
	} else if connectTimeout_, ok := options[OPT_CONNECTTIMEOUT]; ok {
		if connectTimeout, ok := connectTimeout_.(int); ok {
			connectTimeoutMS = connectTimeout * 1000
		} else {
			return nil, fmt.Errorf("OPT_CONNECTTIMEOUT must be int")
		}
	}

	timeoutMS := 0

	if timeoutMS_, ok := options[OPT_TIMEOUT_MS]; ok {
		if timeoutMS, ok = timeoutMS_.(int); !ok {
			return nil, fmt.Errorf("OPT_TIMEOUT_MS must be int")
		}
	} else if timeout_, ok := options[OPT_TIMEOUT]; ok {
		if timeout, ok := timeout_.(int); ok {
			timeoutMS = timeout * 1000
		} else {
			return nil, fmt.Errorf("OPT_TIMEOUT must be int")
		}
	}

	// fix connect timeout(important, or it might cause a long time wait during
	//connection)
	if timeoutMS > 0 && (connectTimeoutMS > timeoutMS || connectTimeoutMS == 0) {
		connectTimeoutMS = timeoutMS
	}

	transport.Dial = func(network, addr string) (net.Conn, error) {
		var conn net.Conn
		var err error
		if connectTimeoutMS > 0 {
			conn, err = net.DialTimeout(network, addr, time.Duration(connectTimeoutMS)*time.Millisecond)
			if err != nil {
				return nil, err
			}
		} else {
			conn, err = net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
		}

		if timeoutMS > 0 {
			conn.SetDeadline(time.Now().Add(time.Duration(timeoutMS) * time.Millisecond))
		}

		return conn, nil
	}

	// proxy
	if proxyFunc_, ok := options[OPT_PROXY_FUNC]; ok {
		if proxyFunc, ok := proxyFunc_.(func(*http.Request) (int, string, error)); ok {
			transport.Proxy = func(req *http.Request) (*url.URL, error) {
				proxyType, u_, err := proxyFunc(req)
				if err != nil {
					return nil, err
				}

				if proxyType != PROXY_HTTP {
					return nil, fmt.Errorf("only PROXY_HTTP is currently supported")
				}

				u_ = "http://" + u_

				u, err := url.Parse(u_)

				if err != nil {
					return nil, err
				}

				return u, nil
			}
		} else {
			return nil, fmt.Errorf("OPT_PROXY_FUNC is not a desired function")
		}
	} else {
		var proxytype int
		if proxytype_, ok := options[OPT_PROXYTYPE]; ok {
			if proxytype, ok = proxytype_.(int); !ok || proxytype != PROXY_HTTP {
				return nil, fmt.Errorf("OPT_PROXYTYPE must be int, and only PROXY_HTTP is currently supported")
			}
		}

		var proxy string
		if proxy_, ok := options[OPT_PROXY]; ok {
			if proxy, ok = proxy_.(string); !ok {
				return nil, fmt.Errorf("OPT_PROXY must be string")
			}
			proxy = "http://" + proxy
			proxyUrl, err := url.Parse(proxy)
			if err != nil {
				return nil, err
			}
			transport.Proxy = http.ProxyURL(proxyUrl)
		}
	}

	return transport, nil
}

// Prepare a redirect policy.
func prepareRedirect(options map[int]interface{}) (func(req *http.Request, via []*http.Request) error, error) {
	var redirectPolicy func(req *http.Request, via []*http.Request) error

	if redirectPolicy_, ok := options[OPT_REDIRECT_POLICY]; ok {
		if redirectPolicy, ok = redirectPolicy_.(func(*http.Request, []*http.Request) error); !ok {
			return nil, fmt.Errorf("OPT_REDIRECT_POLICY is not a desired function")
		}
	} else {
		var followlocation bool
		if followlocation_, ok := options[OPT_FOLLOWLOCATION]; ok {
			if followlocation, ok = followlocation_.(bool); !ok {
				return nil, fmt.Errorf("OPT_FOLLOWLOCATION must be bool")
			}
		}

		var maxredirs int
		if maxredirs_, ok := options[OPT_MAXREDIRS]; ok {
			if maxredirs, ok = maxredirs_.(int); !ok {
				return nil, fmt.Errorf("OPT_MAXREDIRS must be int")
			}
		}

		redirectPolicy = func(req *http.Request, via []*http.Request) error {
			// no follow
			if !followlocation || maxredirs <= 0 {
				return &Error{
					Code:    ERR_REDIRECT_POLICY,
					Message: fmt.Sprintf("redirect not allowed"),
				}
			}

			if len(via) >= maxredirs {
				return &Error{
					Code:    ERR_REDIRECT_POLICY,
					Message: fmt.Sprintf("stopped after %d redirects", len(via)),
				}
			}

			last := via[len(via)-1]
			// keep necessary headers
			// TODO: pass all headers or add other headers?
			if useragent := last.Header.Get("User-Agent"); useragent != "" {
				req.Header.Set("User-Agent", useragent)
			}

			return nil
		}
	}

	return redirectPolicy, nil
}

// Prepare a cookie jar.
func prepareJar(options map[int]interface{}) (http.CookieJar, error) {
	var jar http.CookieJar
	var err error
	if optCookieJar_, ok := options[OPT_COOKIEJAR]; ok {
		// is bool
		if optCookieJar, ok := optCookieJar_.(bool); ok {
			// default jar
			if optCookieJar {
				// TODO: PublicSuffixList
				jar, err = cookiejar.New(nil)
				if err != nil {
					return nil, err
				}
			}
		} else if optCookieJar, ok := optCookieJar_.(http.CookieJar); ok {
			jar = optCookieJar
		} else {
			return nil, fmt.Errorf("invalid cookiejar")
		}
	}

	return jar, nil
}

// Create an HTTP client.
func NewHttpClient() *HttpClient {
	c := &HttpClient{
		reuseTransport: true,
		reuseJar:       true,
	}

	return c
}

// Powerful and easy to use HTTP client.
type HttpClient struct {
	// Default options of this client.
	Options map[int]interface{}

	// Default headers of this client.
	Headers map[string]string

	// Options of current request.
	oneTimeOptions map[int]interface{}

	// Headers of current request.
	oneTimeHeaders map[string]string

	// Cookies of current request.
	oneTimeCookies []*http.Cookie

	// Global transport of this client, might be shared between different
	// requests.
	transport http.RoundTripper

	// Global cookie jar of this client, might be shared between different
	// requests.
	jar http.CookieJar

	// Whether current request should reuse the transport or not.
	reuseTransport bool

	// Whether current request should reuse the cookie jar or not.
	reuseJar bool

	// Make requests of one client concurrent safe.
	lock *sync.Mutex
}

// Set default options and headers.
func (this *HttpClient) Defaults(defaults Map) *HttpClient {
	options, headers := parseMap(defaults)

	// merge options
	if this.Options == nil {
		this.Options = options
	} else {
		for k, v := range options {
			this.Options[k] = v
		}
	}

	// merge headers
	if this.Headers == nil {
		this.Headers = headers
	} else {
		for k, v := range headers {
			this.Headers[k] = v
		}
	}

	return this
}

// Begin marks the begining of a request, it's necessary for concurrent
// requests.
func (this *HttpClient) Begin() *HttpClient {
	if this.lock == nil {
		this.lock = new(sync.Mutex)
	}
	this.lock.Lock()

	return this
}

// Reset the client state so that other requests can begin.
func (this *HttpClient) reset() {
	this.oneTimeOptions = nil
	this.oneTimeHeaders = nil
	this.oneTimeCookies = nil
	this.reuseTransport = true
	this.reuseJar = true

	// nil means the Begin has not been called, asume requests are not
	// concurrent.
	if this.lock != nil {
		this.lock.Unlock()
	}
}

// Temporarily specify an option of the current request.
func (this *HttpClient) WithOption(k int, v interface{}) *HttpClient {
	if this.oneTimeOptions == nil {
		this.oneTimeOptions = make(map[int]interface{})
	}
	this.oneTimeOptions[k] = v

	// Conditions we cann't reuse the transport.
	if !hasOption(k, transportOptions) {
		this.reuseTransport = false
	}

	// Conditions we cann't reuse the cookie jar.
	if !hasOption(k, jarOptions) {
		this.reuseJar = false
	}

	return this
}

// Temporarily specify multiple options of the current request.
func (this *HttpClient) WithOptions(m Map) *HttpClient {
	options, _ := parseMap(m)
	for k, v := range options {
		this.WithOption(k, v)
	}

	return this
}

// Temporarily specify a header of the current request.
func (this *HttpClient) WithHeader(k string, v string) *HttpClient {
	if this.oneTimeHeaders == nil {
		this.oneTimeHeaders = make(map[string]string)
	}
	this.oneTimeHeaders[k] = v

	return this
}

// Temporarily specify multiple headers of the current request.
func (this *HttpClient) WithHeaders(m map[string]string) *HttpClient {
	for k, v := range m {
		this.WithHeader(k, v)
	}

	return this
}

// Specify cookies of the current request.
func (this *HttpClient) WithCookie(cookies ...*http.Cookie) *HttpClient {
	this.oneTimeCookies = append(this.oneTimeCookies, cookies...)

	return this
}

// Start a request, and get the response.
//
// Usually we just need the Get and Post method.
func (this *HttpClient) Do(method string, url string, headers map[string]string,
	body io.Reader) (*Response, error) {
	options := mergeOptions(defaultOptions, this.Options, this.oneTimeOptions)
	headers = mergeHeaders(this.Headers, this.oneTimeHeaders, headers)
	cookies := this.oneTimeCookies

	var transport http.RoundTripper
	var jar http.CookieJar
	var err error

	// transport
	if this.transport == nil || !this.reuseTransport {
		transport, err = prepareTransport(options)
		if err != nil {
			this.reset()
			return nil, err
		}

		if this.reuseTransport {
			this.transport = transport
		}
	} else {
		transport = this.transport
	}

	// jar
	if this.jar == nil || !this.reuseJar {
		jar, err = prepareJar(options)
		if err != nil {
			this.reset()
			return nil, err
		}

		if this.reuseJar {
			this.jar = jar
		}
	} else {
		jar = this.jar
	}

	// release lock
	this.reset()

	redirect, err := prepareRedirect(options)
	if err != nil {
		return nil, err
	}

	c := &http.Client{
		Transport:     transport,
		CheckRedirect: redirect,
		Jar:           jar,
	}

	req, err := prepareRequest(method, url, headers, body, options)
	if err != nil {
		return nil, err
	}

	if jar != nil {
		jar.SetCookies(req.URL, cookies)
	} else {
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
	}

	res, err := c.Do(req)

	return &Response{res}, err
}

// The HEAD request
func (this *HttpClient) Head(url string, params map[string]string) (*Response,
	error) {
	url = addParams(url, params)

	return this.Do("HEAD", url, nil, nil)
}

// The GET request
func (this *HttpClient) Get(url string, params map[string]string) (*Response,
	error) {
	url = addParams(url, params)

	return this.Do("GET", url, nil, nil)
}

// The POST request
//
// With multipart set to true, the request will be encoded as
// "multipart/form-data".
//
// If any of the params key starts with "@", it is considered as a form file
// (similar to CURL but different).
func (this *HttpClient) Post(url string, params map[string]interface{}) (*Response, error) {
	// Post with files should be sent as multipart.
	if checkParamFile(params) {
		return this.PostMultipart(url, params)
	}

	headers := make(map[string]string)
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	body := strings.NewReader(paramsConvertToString(params))
	return this.Do("POST", url, headers, body)
}

// Post with the request encoded as "multipart/form-data".
func (this *HttpClient) PostMultipart(url string, params map[string]interface{}) (
	*Response, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// check files
	for k, v := range params {
		// is file
		if k[0] == '@' {
			err := addFormFile(writer, k[1:], v.(string))
			if err != nil {
				return nil, err
			}
		} else {
			if k[0] == '#' {
				err := addFormFileFromData(writer, k[1:], "h.img", v.([]byte))
				if err != nil {
					return nil, err
				}
			} else {
				writer.WriteField(k, v.(string))
			}
		}
	}
	headers := make(map[string]string)
	headers["Content-Type"] = writer.FormDataContentType()
	err := writer.Close()
	if err != nil {
		return nil, err
	}
	return this.Do("POST", url, headers, body)
}

// Get cookies of the client jar.
func (this *HttpClient) Cookies(url_ string) []*http.Cookie {
	if this.jar != nil {
		u, _ := url.Parse(url_)
		return this.jar.Cookies(u)
	}

	return nil
}

// Get cookie values(k-v map) of the client jar.
func (this *HttpClient) CookieValues(url_ string) map[string]string {
	m := make(map[string]string)

	for _, c := range this.Cookies(url_) {
		m[c.Name] = c.Value
	}

	return m
}

// Get cookie value of a specified cookie name.
func (this *HttpClient) CookieValue(url_ string, key string) string {
	for _, c := range this.Cookies(url_) {
		if c.Name == key {
			return c.Value
		}
	}

	return ""
}
