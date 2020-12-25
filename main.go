package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

var (
	isClient     = flag.Bool("client", false, "server or client")
	addr         = flag.String("addr", "http://127.0.0.1:80", "host:port (only for client mode)")
	batchSize    = flag.Int("size", 20, "batch size")
	batchCount   = flag.Int("count", 10, "batch count")
	reqSize      = flag.Int("reqsize", 0, "request size")
	verbose      = flag.Bool("verbose", false, "verbose")
	respSize     = flag.Int("respsize", 0, "response size")
	dry          = flag.Bool("dry", false, "dry run")
	local        = flag.Bool("local", false, "local test")
	output       = flag.String("output", "", "filename")
	report       = flag.Bool("report", false, "report to server")
	reportPrefix = flag.String("reportPrefix", "", "report prefix for server")
	onlyHit      = flag.Bool("onlyHit", false, "count only hit request(CF only)")
)

func main() {
	flag.Parse()
	fmt.Printf("client mode: %t\n", *isClient)
	if *isClient {
		fmt.Printf("host: %s\n", *addr)
		client()
	} else {
		done := make(chan bool)
		go server()
		if *local {
			time.Sleep(100 * time.Millisecond)
			client()
		}
		<-done
	}
}

var wg sync.WaitGroup

func client() {
	fmt.Printf("client mode, client: %s\n", *addr)

	tr := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
	}
	c := &http.Client{Transport: tr}

	printer := make(chan []int64, 1024)
	done := make(chan bool)
	go print(c, printer, done)

	for i := 0; i < *batchCount; i++ {
		for j := 0; j < *batchSize; j++ {
			pass, res := clientCall(c)
			if !pass {
				printer <- res
			}
		}
		tr.CloseIdleConnections()
	}
	done <- true
	wg.Wait()
}

func clientCall(client *http.Client) (bool, []int64) {
	var err error
	var resp *http.Response
	var respTimestamp int64
	var reqTimestamp int64
	if *reqSize != 0 || *respSize != 0 {
		form := url.Values{"payload": {strings.Repeat("0", *reqSize)}, "respSize": {strconv.Itoa(*respSize)}}
		reqTimestamp = time.Now().UnixNano() / 1000
		resp, err = client.PostForm(*addr, form)
		respTimestamp = time.Now().UnixNano() / 1000
	} else {
		reqTimestamp = time.Now().UnixNano() / 1000
		resp, err = client.Get(*addr)
		respTimestamp = time.Now().UnixNano() / 1000
	}

	if err != nil {
		panic(err)
	}

	if *onlyHit {
		if strings.Contains(resp.Header.Get("X-Cache"), "Miss") {
			return true, []int64{}
		}
	}

	body, _ := ioutil.ReadAll(resp.Body)
	bodyString := string(body)
	resp.Body.Close()

	chunks := strings.SplitN(bodyString, "|", 3)

	timestamp := chunks[0]
	reqCount := chunks[1]

	var first int64
	if reqCount == "1" {
		first = 1
	} else {
		first = 0
	}

	serverTimestamp, _ := strconv.ParseInt(timestamp, 10, 64)

	reqToSvrDiff := serverTimestamp - reqTimestamp
	svrToRespDiff := respTimestamp - serverTimestamp
	totalDiff := respTimestamp - reqTimestamp

	return false, []int64{first, totalDiff, reqToSvrDiff, svrToRespDiff}
}

func print(c *http.Client, printer chan []int64, done chan bool) {
	wg.Add(1)
	var filename string
	if *output != "" {
		filename = *output
	} else {
		filename = fmt.Sprintf("output-%s.csv", time.Now().Format("2006.01.02 15:04"))
	}
	file, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	var writer *csv.Writer
	if *dry == false {
		writer = csv.NewWriter(bufio.NewWriter(file))
		writer.Write([]string{"isFirst", "diff", "client to server", "server to client"})
	}

	for {
		select {
		case datas := <-printer:
			var first string
			if datas[0] == 1 {
				first = "true"
			} else {
				first = "false"
			}
			diff := strconv.FormatInt(datas[1], 10)
			cts := strconv.FormatInt(datas[2], 10)
			stc := strconv.FormatInt(datas[3], 10)

			if *verbose {
				fmt.Printf("%s %s %s %s\n", first, diff, cts, stc)
			}

			if *dry == false {
				writer.Write([]string{first, diff, cts, stc})
				writer.Flush()
			}
		case <-done:
			file.Close()

			data, _ := ioutil.ReadFile(filename)
			csv := string(data)

			if *report {
				form := url.Values{"csv": {csv}, "filename": {filename}}
				url := fmt.Sprintf("%s/report", *addr)
				c.PostForm(url, form)
			}

			wg.Done()
			return
		}
	}
}

func server() {
	fmt.Println("server mode")
	r := router.New()
	r.POST("/", requestHandler)
	r.GET("/", requestHandler)

	r.POST("/report", reportHandler)

	s := &fasthttp.Server{
		Handler:      r.Handler,
		TCPKeepalive: true,
	}

	if err := s.ListenAndServe(":80"); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}

var reporter chan string

func requestHandler(ctx *fasthttp.RequestCtx) {
	now := time.Now().UnixNano() / 1000
	respSizeStr := string(ctx.FormValue("respSize"))
	respSize, _ := strconv.Atoi(respSizeStr)

	fmt.Fprintf(ctx, "%d|%d", now, ctx.ConnRequestNum())
	if respSize != 0 {
		fmt.Fprint(ctx, "|")
		fmt.Fprintln(ctx, strings.Repeat("0", respSize))
	}
}

func reportHandler(ctx *fasthttp.RequestCtx) {
	csv := string(ctx.FormValue("csv"))
	filename := string(ctx.FormValue("filename"))

	filename = fmt.Sprintf("%s%s", *reportPrefix, filename)

	file, _ := os.Create(filename)
	file.WriteString(csv)
	file.Close()

	fmt.Fprintln(ctx, "OK")
}
