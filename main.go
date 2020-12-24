package main

import (
	"net/http"
	"net/url"
	"io/ioutil"
	"encoding/csv"
	"strconv"
	"strings"
	"bufio"
	"flag"
	"time"
	"log"
	"fmt"
	"os"
	"github.com/valyala/fasthttp"
)

var (
	isClient = flag.Bool("client", false, "server or client")
	addr = flag.String("addr", "http://127.0.0.1:80", "host:port (only for client mode)")
	batchSize = flag.Int("size", 20, "batch size")
	batchCount = flag.Int("count", 10, "batch count")
	reqSize = flag.Int("reqsize", 0, "request size")
	verbose = flag.Bool("verbose", false, "verbose")
	respSize = flag.Int("respsize", 0, "response size")
	dry = flag.Bool("dry", false, "dry run")
	local = flag.Bool("local", false, "local test")
)

func main() {
	flag.Parse()
	fmt.Printf("client mode: %t\n", *isClient)
	if (*isClient) {
		fmt.Printf("host: %s\n", *addr)
		client()
	} else {
		done := make(chan bool)
		go server()
		if (*local) {
			time.Sleep(100 * time.Millisecond)
			client()
		}
		<- done
	}
}

func client() {
	fmt.Printf("client mode, client: %s\n", *addr)

	printer := make(chan []int64, 1024)
	go print(printer)

	tr := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
	}
	client := &http.Client{Transport: tr}

	for i := 0; i < *batchCount; i++ {
		for j := 0; j < *batchSize; j++ {
			res := clientCall(client)
			printer <- res
		}
		tr.CloseIdleConnections()
	}
}

func clientCall(client *http.Client) []int64 {
	form := url.Values{"payload": {strings.Repeat("0", *reqSize)}}

	reqTimestamp := time.Now().UnixNano() / 1000
	resp, err := client.PostForm(*addr, form)
	respTimestamp := time.Now().UnixNano() / 1000
	if err != nil {
		panic(err)
	}

	body, _ := ioutil.ReadAll(resp.Body)
	bodyString := string(body)
	resp.Body.Close()

	chunks := strings.SplitN(bodyString,"|", 3)

	timestamp := chunks[0]
	reqCount := chunks[1]

	var first int64
	if (reqCount == "1") {
		first = 1
	} else {
		first = 0
	}

	serverTimestamp, _ := strconv.ParseInt(timestamp, 10, 64)

	reqToSvrDiff := serverTimestamp - reqTimestamp
	svrToRespDiff := respTimestamp - serverTimestamp
	totalDiff := respTimestamp - reqTimestamp

	return []int64{first, totalDiff, reqToSvrDiff, svrToRespDiff}
}

func print(printer chan []int64) {
	filename := fmt.Sprintf("output-%s.csv", time.Now().Format("2006.01.02 15:04"))
	file, err := os.Create(filename)
    if err != nil {
        panic(err)
	}
	var writer *csv.Writer
	if (*dry == false) {
		writer = csv.NewWriter(bufio.NewWriter(file))
		writer.Write([]string{"isFirst", "diff", "client to server", "server to client"})
	}

	for datas := range printer {
		var first string
		if datas[0] == 1 {
			first = "true"
		} else {
			first = "false"
		}
		diff := strconv.FormatInt(datas[1], 10)
		cts := strconv.FormatInt(datas[2], 10)
		stc := strconv.FormatInt(datas[3], 10)
 
		if (*verbose) {
			fmt.Printf("%s %s %s %s\n", first, diff, cts, stc)
		}

		if (*dry == false) {
			writer.Write([]string{first, diff, cts, stc})
			writer.Flush()
		}
	}
}

func server() {
	fmt.Println("server mode")
	h := requestHandler

	s := &fasthttp.Server{
		Handler: h,
		TCPKeepalive: true,
	}

	if err := s.ListenAndServe(":80"); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	now := time.Now().UnixNano() / 1000

	fmt.Fprintf(ctx, "%d|%d", now, ctx.ConnRequestNum())
	if (*respSize != 0) {
		fmt.Fprint(ctx, "|")
		fmt.Fprintln(ctx, strings.Repeat("0", *respSize))
	}
}
