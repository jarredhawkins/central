package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// triggersKey follows [action][stock][user] indexing
type triggersKey struct {
	action, stock, user string
}

var waitingTriggers = make(map[triggersKey]trigger)
var waitingTriggersLock sync.Mutex
var runningTriggers = make(map[triggersKey]trigger)
var runningTriggersLock sync.Mutex

var successListener = make(chan trigger)

func main() {
	fmt.Println("Launching server...")
	http.HandleFunc("/setTrigger", setTriggerHandler)
	http.HandleFunc("/startTrigger", startTriggerHandler)
	http.HandleFunc("/cancelTrigger", cancelTriggerHandler)
	http.HandleFunc("/runningTriggers", getRunningTriggersHandler)
	http.HandleFunc("/waitingTriggers", getWaitingTriggersHandler)

	go startSuccessListener()

	fmt.Printf("Trigger server listening on %s:%s\n", os.Getenv("triggeraddr"), os.Getenv("triggerport"))
	if err := http.ListenAndServe(":"+os.Getenv("triggerport"), nil); err != nil {
		panic(err)
	}
}

func startTriggerHandler(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("action")
	//transnumStr := r.FormValue("transnum")
	username := r.FormValue("username")
	stock := r.FormValue("stock")
	priceStr := r.FormValue("price")

	//transnum, err := strconv.Atoi(transnumStr)
	//if err != nil {
	//	w.WriteHeader(http.StatusBadRequest)
	//	panic(err)
	//}

	price, err := decimal.NewFromString(priceStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		panic(err)
	}

	// START LOCKING -- BE CAREFUL OF DEADLOCKS HERE
	waitingTriggersLock.Lock()
	t := waitingTriggers[triggersKey{action, stock, username}]
	t.price = price

	runningTriggersLock.Lock()

	delete(waitingTriggers, triggersKey{t.action, t.stockname, t.username})
	runningTriggers[triggersKey{t.action, t.stockname, t.username}] = t

	go t.StartPolling()

	waitingTriggersLock.Unlock()
	runningTriggersLock.Unlock()
	// END LOCKING

	w.WriteHeader(http.StatusOK)
}

func setTriggerHandler(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("action")
	transnumStr := r.FormValue("transnum")
	username := r.FormValue("username")
	stock := r.FormValue("stock")
	amountStr := r.FormValue("amount")

	if !verifyAction(action) {
		w.WriteHeader(http.StatusBadRequest)
		panic("Tried to post a bad action (BUY/SELL)")
	}

	transnum, err := strconv.Atoi(transnumStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		panic(err)
	}

	amount, err := strconv.Atoi(amountStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		panic(err)
	}

	var t trigger
	if action == "BUY" {
		t = newBuyTrigger(successListener, transnum, username, stock, amount)
	} else {
		t = newSellTrigger(successListener, transnum, username, stock, amount)
	}

	waitingTriggersLock.Lock()
	waitingTriggers[triggersKey{t.action, t.stockname, t.username}] = t
	waitingTriggersLock.Unlock()
	fmt.Println("Added but not started: ", t)

	w.WriteHeader(http.StatusOK)
}

func cancelTriggerHandler(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("action")
	//transnumStr := r.FormValue("transnum")
	username := r.FormValue("username")
	stock := r.FormValue("stock")

	if !verifyAction(action) {
		w.WriteHeader(http.StatusBadRequest)
		panic("Tried to post a bad action (BUY/SELL)")
	}

	//transnum, err := strconv.Atoi(transnumStr)
	//if err != nil {
	//	w.WriteHeader(http.StatusBadRequest)
	//	panic(err)
	//}

	cancelledTrigger, err := cancelTrigger(triggersKey{action, stock, username})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		panic(err)
	}
	fmt.Println("CANCELLED: ", cancelledTrigger)
	w.Write([]byte(cancelledTrigger.String()))
}

func startSuccessListener() {
	for {
		select {
		case trig := <-successListener:
			fmt.Println("Closing successful trigger: ", trig)
			go cancelTrigger(trig)
			go alertTriggerSuccess(trig)
		}
	}
}

// Send an alert back to the transaction server when a trigger successfully finishes
func alertTriggerSuccess(t trigger) {
	conn, err := net.DialTimeout("tcp",
		os.Getenv("transaddr")+":"+os.Getenv("transport"),
		time.Second*15,
	)
	if err != nil { // trans server down? retry
		panic(err)
	}

	fmt.Println(t.getSuccessString())
	_, err = fmt.Fprintf(conn, t.getSuccessString())
	if err != nil {
		panic(err)
	}

	err = conn.Close()
	if err != nil {
		panic(err)
	}
}

func getRunningTriggersHandler(w http.ResponseWriter, r *http.Request) {
	runningTriggersLock.Lock()
	fmt.Fprintln(w, runningTriggers)
	runningTriggersLock.Unlock()
}

func getWaitingTriggersHandler(w http.ResponseWriter, r *http.Request) {
	waitingTriggersLock.Lock()
	fmt.Fprintln(w, waitingTriggers)
	waitingTriggersLock.Unlock()
}

func verifyAction(action string) bool {
	if action != "BUY" && action != "SELL" {
		return false
	}
	return true
}

// Removes the trigger from the poller, returns the removed key and any errors
func cancelTrigger(t triggersKey) (trigger, error) {
	runningTriggersLock.Lock()
	waitingTriggersLock.Lock()
	defer runningTriggersLock.Unlock()
	defer waitingTriggersLock.Unlock()

	trigger, running := runningTriggers[t]
	if running {
		runningTriggers[t].Cancel()
		delete(runningTriggers, t)
		return trigger, nil
	}

	trigger, waiting := waitingTriggers[t]
	if waiting {
		delete(waitingTriggers, t)
		return trigger, nil
	}

	return trigger, errors.New("Can't find waiting or running trigger to cancel")
}
