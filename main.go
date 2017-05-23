package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

const EVENT_PUSH_TIMEOUT = 6 * time.Hour

var cookies []*http.Cookie = []*http.Cookie{}

type CoinType string

const (
	BTC = "btc"
	ETH = "eth"
)

func requestPrice(ct CoinType, retry int) (float64, error) {
	if retry == 0 {
		return 0, fmt.Errorf("retry timeout")
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("https://www.maicoin.com/api/prices/%s-usd/", ct), nil)
	req.Header.Add("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.36")
	req.Header.Add("accept", "*/*")
	req.Header.Add("host", "www.maicoin.com")
	req.Header.Add("cache-control", "no-cache")

	for _, c := range cookies {
		req.AddCookie(c)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("maicoin request error: %s", err.Error())
	}
	defer resp.Body.Close()

	d := json.NewDecoder(resp.Body)
	v := map[string]interface{}{}

	err = d.Decode(&v)
	if err != nil {
		cookies = resp.Cookies()
		time.Sleep(time.Second)
		log.Printf("parse error. sleep for 1 second. error: %s", err.Error())
		return requestPrice(ct, retry-1)
	}

	rawPrice, okKey := v["raw_price_in_twd"]
	if !okKey {
		return 0, fmt.Errorf("missing field")
	}

	price, okVal := rawPrice.(float64)
	if !okVal {
		return 0, fmt.Errorf("incorrect price type")
	}
	price /= 100000
	return price, nil
}

func pushIFTTTEvent(ct CoinType, price, lastPrice float64, iftttToken string) error {
	priceRatio := price / lastPrice
	changeText := ""
	if priceRatio > 1 {
		changeText = "📈"
	} else {
		changeText = "📉"
	}
	postBody := map[string]string{
		"value1": changeText,
		"value2": fmt.Sprintf("%s %0.4f", strings.ToUpper(string(ct)), price),
		"value3": fmt.Sprintf("%+0.2f", (priceRatio-1)*100),
	}

	buf := bytes.Buffer{}
	e := json.NewEncoder(&buf)
	err := e.Encode(postBody)
	if err != nil {
		return err
	}

	r, err := http.Post(
		fmt.Sprintf("https://maker.ifttt.com/trigger/%s/with/key/%s", ct, iftttToken),
		"application/json", &buf)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("invalid status return of ifttt event call: %s", r.Status)
	}
	return nil
}

type PriceInfo struct {
	Current float64
	Last    float64
}

func alertPrice(ct CoinType, percentThreshold, unitThreshold float64) <-chan *PriceInfo {
	var currentPrice = 0.0
	var lastPrice = 0.0
	ch := make(chan *PriceInfo)

	var err error
	go func() {
		for {
			currentPrice, err = requestPrice(ct, 3)
			log.Printf("Current price: %0.4f. Last price: %0.4f", currentPrice, lastPrice)
			if err != nil {
				log.Print(err)
			} else {
				priceDiff := currentPrice - lastPrice
				priceRatio := currentPrice / lastPrice

				if math.Abs(priceRatio) > percentThreshold || math.Abs(priceDiff) > unitThreshold {
					log.Print("The difference of two price exceed the threshold. Push a new event.")
					ch <- &PriceInfo{Current: currentPrice, Last: lastPrice}
					lastPrice = currentPrice
				}
			}
			time.Sleep(time.Minute)
		}
	}()

	return ch
}

func main() {
	iftttToken := flag.String("iftttToken", "", "ifttt maker token")
	flag.Parse()

	ethAlert := alertPrice(ETH, 5, 200)
	btcAlert := alertPrice(BTC, 5, 2000)

	for {
		select {
		case <-time.After(EVENT_PUSH_TIMEOUT):
		case price := <-ethAlert:
			if err := pushIFTTTEvent(ETH, price.Current, price.Last, *iftttToken); err != nil {
				log.Printf("IFTTT error: %s", err.Error())
			}
		case price := <-btcAlert:
			if err := pushIFTTTEvent(BTC, price.Current, price.Last, *iftttToken); err != nil {
				log.Printf("IFTTT error: %s", err.Error())
			}
		}
	}
}
