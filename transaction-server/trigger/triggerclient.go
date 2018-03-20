package triggerclient

import (
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"

	"github.com/shopspring/decimal"
)

var triggeraddr = os.Getenv("triggeraddr")
var triggerport = os.Getenv("triggerport")
var triggerURL = "http://" + triggeraddr + ":" + triggerport

const (
	setEndpoint    = "/setTrigger"
	startEndpoint  = "/startTrigger"
	cancelEndpoint = "/cancelTrigger"
	listEndpoint   = "/runningTriggers"
)

// TriggerFunctions are all of the functionality needed to support the trigger
type TriggerFunctions interface {
	SetNewSellTrigger(transNum int, username string, stock string, amount decimal.Decimal) error
	SetSellTrigger(transNum int, trig trigger) error
	StartSellTrigger(transNum int, trig trigger) error
	StartNewSellTrigger(transNum int, username string, stock string, price decimal.Decimal) error
	CancelSellTrigger(transNum int, username string, stock string) (trigger, error)

	SetNewBuyTrigger(transNum int, username string, stock string, amount decimal.Decimal) error
	SetBuyTrigger(transNum int, trig trigger) error
	StartBuyTrigger(transNum int, trig trigger) error
	StartNewBuyTrigger(transNum int, username string, stock string, price decimal.Decimal) error
	CancelBuyTrigger(transNum int, username string, stock string) (trigger, error)

	ListRunningTriggers()
}

// TriggerClient acts as an interface for the trigger server
type TriggerClient struct {
}

// SetNewSellTrigger adds a new sell trigger to the triggerserver
func (tc TriggerClient) SetNewSellTrigger(transNum int, username string, stock string, amount decimal.Decimal) error {
	trig := newSellTrigger(transNum, username, stock, amount)
	return tc.setTrigger(transNum, trig)
}

// SetSellTrigger adds a new sell trigger to the triggerserver
func (tc TriggerClient) SetSellTrigger(transNum int, trig trigger) error {
	return tc.setTrigger(transNum, trig)
}

// StartSellTrigger adds a new sell trigger to the triggerserver
func (tc TriggerClient) StartSellTrigger(transNum int, trig trigger) error {
	return tc.startTrigger(transNum, trig)
}

// StartNewSellTrigger starts an existing sell trigger on the triggerserver
func (tc TriggerClient) StartNewSellTrigger(transNum int, username string, stock string, price decimal.Decimal) error {
	trig := trigger{
		transNum:  transNum,
		username:  username,
		stockname: stock,
		price:     price,
		action:    "SELL",
	}
	return tc.startTrigger(transNum, trig)
}

// CancelSellTrigger attempts to cancel an existing sell trigger on the server
func (tc TriggerClient) CancelSellTrigger(transNum int, username string, stock string) (trigger, error) {
	trig := trigger{
		transNum:  transNum,
		username:  username,
		stockname: stock,
		action:    "SELL",
	}
	return tc.cancelTrigger(transNum, trig)
}

// SetNewBuyTrigger adds a new sell trigger to the triggerserver
func (tc TriggerClient) SetNewBuyTrigger(transNum int, username string, stock string, amount decimal.Decimal) error {
	trig := newBuyTrigger(transNum, username, stock, amount)
	return tc.setTrigger(transNum, trig)
}

// SetBuyTrigger adds a new Buy trigger to the triggerserver
func (tc TriggerClient) SetBuyTrigger(transNum int, trig trigger) error {
	return tc.setTrigger(transNum, trig)
}

// StartBuyTrigger adds a new buy trigger to the triggerserver
func (tc TriggerClient) StartBuyTrigger(transNum int, trig trigger) error {
	return tc.startTrigger(transNum, trig)
}

// StartNewBuyTrigger starts an existing Buy trigger on the triggerserver
func (tc TriggerClient) StartNewBuyTrigger(transNum int, username string, stock string, price decimal.Decimal) error {
	trig := trigger{
		transNum:  transNum,
		username:  username,
		stockname: stock,
		price:     price,
		action:    "BUY",
	}
	return tc.startTrigger(transNum, trig)
}

// Attempts to cancel an existing Buy trigger on the server
func (tc TriggerClient) CancelBuyTrigger(transNum int, username string, stock string) (trigger, error) {
	trig := trigger{
		transNum:  transNum,
		username:  username,
		stockname: stock,
		action:    "BUY",
	}
	return tc.cancelTrigger(transNum, trig)
}

// setTrigger adds a new trigger to the triggerserver.
// Action is either 'BUY' or 'SELL'
func (tc TriggerClient) setTrigger(transNum int, newTrigger trigger) error {
	values := url.Values{
		"action":   {newTrigger.action},
		"transnum": {strconv.Itoa(transNum)},
		"username": {newTrigger.username},
		"stock":    {newTrigger.stockname},
		"amount":   {newTrigger.getAmountStr()},
	}
	_, err := http.PostForm(triggerURL+startEndpoint, values)
	if err != nil {
		return err
	}

	return nil
}

// startTrigger starts an existing trigger on the triggerserver.
func (tc TriggerClient) startTrigger(transNum int, newTrigger trigger) error {
	values := url.Values{
		"action":   {newTrigger.action},
		"transnum": {strconv.Itoa(transNum)},
		"username": {newTrigger.username},
		"stock":    {newTrigger.stockname},
		"price":    {newTrigger.getPriceStr()},
	}
	_, err := http.PostForm(triggerURL+setEndpoint, values)
	if err != nil {
		return err
	}

	return nil
}

// CancelTrigger cancels a running trigger on the triggerserver.
// Action is either 'BUY' or 'SELL'
// If the given trigger could not be found, returns an error.
// Should return the cancelled triggers details
func (tc TriggerClient) cancelTrigger(transNum int, cancel trigger) (trigger, error) {
	values := url.Values{
		"action":   {cancel.action},
		"transnum": {strconv.Itoa(transNum)},
		"username": {cancel.username},
		"stock":    {cancel.stockname},
	}
	resp, err := http.PostForm(triggerURL+cancelEndpoint, values)
	if err != nil {
		return trigger{}, err
	}

	trig := tc.getTriggerFromResponse(resp)
	return trig, nil
}

// ListRunningTriggers returns a list of all running triggers on the TriggerServer
// TODO: something useful if needed
func (tc TriggerClient) ListRunningTriggers() {
	_, err := http.Get(triggerURL + listEndpoint)
	if err != nil {
		panic(err)
	}
}

func (tc TriggerClient) getTriggerFromResponse(resp *http.Response) trigger {
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	bodyString := string(bodyBytes)
	return tc.parseTriggerFromString(bodyString)
}

// "{%v %v %v %v %v}", t.username, t.stockname, t.getPriceStr(), t.amount, t.action
func (tc TriggerClient) parseTriggerFromString(trigStr string) trigger {
	re := regexp.MustCompile(`{(\w+) (\w+) (\d+.\d+) (\d+.\d+) (\w+)}`)
	matches := re.FindStringSubmatch(trigStr)

	price, err := decimal.NewFromString(matches[3])
	if err != nil {
		panic(err)
	}
	amount, err := decimal.NewFromString(matches[4])
	if err != nil {
		panic(err)
	}

	trig := trigger{
		username:  matches[1],
		stockname: matches[2],
		price:     price,
		amount:    amount,
		action:    matches[5],
	}
	return trig
}
