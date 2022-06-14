package main

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	json2 "helloworld/json"
)

var influxFlushInterval = 5 * time.Second

type server struct {
	api.WriteAPI
}

func main() {

	var (
		influxENDPOINT = os.Getenv("INFLUX_ENDPOINT")
		influxTOKEN    = os.Getenv("INFLUX_TOKEN")
		influxORG      = os.Getenv("INFLUX_ORG")
		influxBUCKET   = os.Getenv("INFLUX_BUCKET")
	)

	if influxENDPOINT == "" {
		log.Fatal("missing required influx config: " + "influx ENDPOINT")
	}
	if influxTOKEN == "" {
		log.Fatal("missing required influx config: " + "influx TOKEN")
	}
	if influxORG == "" {
		log.Fatal("missing required influx config: " + "influx ORG")
	}
	if influxBUCKET == "" {
		log.Fatal("missing required influx config: " + "influx BUCKET")
	}

	client := influxdb2.NewClient(
		influxENDPOINT,
		influxTOKEN)
	defer client.Close()

	write := client.WriteAPI(
		influxORG,
		influxBUCKET)

	errorsCh := write.Errors()
	go func() {
		for err := range errorsCh {
			log.Println("influx write API error: " + err.Error())
		}
	}()

	go func() {
		t := time.NewTicker(influxFlushInterval)
		for range t.C {
			write.Flush()
		}
	}()

	s := server{write}

	r := gin.New()

	r.POST("/", s.handlePOST)

	// [START setting_port]
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}
	r.Run(":" + port)
}

func (s server) handlePOST(c *gin.Context) {

	ts := time.Now()

	requestTags := map[string]string{
		"origin_url": c.Request.Header.Get("X-CF-URL"),
		"country":    c.Request.Header.Get("X-CF-COUNTRY"),
	}
	requestFields := map[string]interface{}{
		"value": 1,
	}

	// requestPoint represents a measurement
	// of the HTTP request, but does not measure
	// JSONRPC calls (the parsed body content).
	requestPoint := influxdb2.NewPoint(
		/* measurement */ "request",
		/* tags */ requestTags,
		/* fields */ requestFields,
		/* timestamp */ ts)
	defer s.WritePoint(requestPoint)

	if cacheHits, err := strconv.Atoi(c.Request.Header.Get("X-CF-CACHEHITS")); err == nil {
		requestPoint.AddField("cache_hits", cacheHits)
	}

	var raw json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		b, _ := io.ReadAll(c.Request.Body)
		requestPoint.AddField("invalid_json", true)
		requestPoint.AddField("size", len(b))
		return
	}

	msgs, isBatch := json2.ParseMessage(raw)
	requestPoint.AddField("batch", isBatch)
	if isBatch {
		requestPoint.AddField("batch_size", len(msgs))
	}

	// For each JSONRPC call we'll create
	// a new influx point.
	for _, msg := range msgs {
		callPoint := influxdb2.NewPoint(
			"call:"+msg.Method,
			requestTags,
			requestFields,
			ts,
		)
		callPoint.AddField("value", 1)
		s.WritePoint(callPoint)
	}
}
