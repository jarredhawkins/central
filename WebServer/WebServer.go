package main

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"seng468/WebServer/Commands"
	"sync/atomic"
	"time"

	"golang.org/x/sync/syncmap"

	"seng468/WebServer/UserSessions"
	"seng468/WebServer/logger"
	"seng468/WebServer/transmitter"
	"strings"
	// _ "net/http/pprof"
)

type WebServer struct {
	Name              string
	transactionNumber int64
	userSessions      *syncmap.Map
	transmitter       *transmitter.Transmitter
	logger            logger.Logger
	validPath         *regexp.Regexp
}

func (webServer *WebServer) makeHandler(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		m := webServer.validPath.FindStringSubmatch(request.URL.Path)
		if m == nil {
			http.NotFound(writer, request)
		} else {
			fn(writer, request, m[1])
		}
	}
}

// Garuntees that the user exists in the session cache for managing operations
func (webServer *WebServer) loginHandler(writer http.ResponseWriter, request *http.Request) {
	userName := request.FormValue("username")
	if _, ok := webServer.userSessions.Load(userName); !ok {
		webServer.userSessions.Store(userName, usersessions.NewUserSession(userName))
	}
}

func (webServer *WebServer) addHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	amount := request.FormValue("amount")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "ADD", username, nil, nil, amount)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "ADD,"+username+","+amount)
	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) quoteHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "QUOTE",
		username, stock, nil, nil)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "QUOTE,"+username+","+stock)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
	writer.Write([]byte(resp))
}

func (webServer *WebServer) buyHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")
	amount := request.FormValue("amount")
	command := commands.NewCommand("BUY", username, []string{stock, amount})

	webServer.logger.UserCommand(webServer.Name, currTransNum, "BUY",
		username, stock, nil, amount)

	val, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Invalid request", 400)
		return
	}
	userSession := val.(*usersessions.UserSession)

	resp := webServer.transmitter.MakeRequest(currTransNum, "BUY,"+username+","+stock+","+amount)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}

	// Append buy to pendingBuys list
	userSession.PendingBuys = append(userSession.PendingBuys, command)
}

func (webServer *WebServer) commitBuyHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "COMMIT_BUY",
		username, nil, nil, nil)

	val, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}
	userSession := val.(*usersessions.UserSession)

	if !userSession.HasPendingBuys() {
		// No pendings buys, return error
		go webServer.logger.SystemError(webServer.Name, currTransNum, "COMMIT_BUY",
			username, nil, nil, nil, "No pending buys to commit")
		http.Error(writer, "No pending buys to commit", 400)
		return
	}

	lastBuyCommand := userSession.PendingBuys[0]
	var resp string
	if lastBuyCommand.HasTimeElapsed() {
		// Time has elapsed on Buy, automatically cancel request
		resp = webServer.transmitter.MakeRequest(currTransNum, "CANCEL_BUY,"+username)
		webServer.logger.SystemError(webServer.Name, currTransNum, "COMMIT_BUY",
			username, nil, nil, nil, "Time elapsed on most recent buy request")
		http.Error(writer, "Time elapsed on most recent buy request", 400)
		if len(userSession.PendingBuys) <= 1 {
			// clear the list
			userSession.PendingBuys = nil
		} else {
			// Pop last sell off the pending list.
			userSession.PendingBuys = userSession.PendingBuys[1:]
		}
		return
	} else {
		resp = webServer.transmitter.MakeRequest(currTransNum, "COMMIT_BUY,"+username)
	}

	if resp == "-1" {
		http.Error(writer, "Invalid request", 400)
		return
	}

	if len(userSession.PendingBuys) <= 1 {
		// clear the list
		userSession.PendingBuys = nil
	} else {
		// Pop last sell off the pending list.
		userSession.PendingBuys = userSession.PendingBuys[1:]
	}
}

func (webServer *WebServer) cancelBuyHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "CANCEL_BUY",
		username, nil, nil, nil)

	val, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}
	userSession := val.(*usersessions.UserSession)

	if !userSession.HasPendingBuys() {
		webServer.logger.SystemError(webServer.Name, currTransNum, "CANCEL_BUY",
			username, nil, nil, nil, "No pending buys to cancel")
		http.Error(writer, "No pending buys to cancel", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "CANCEL_BUY,"+username)

	if resp == "-1" {
		http.Error(writer, "Invalid request", 400)
		return
	}

	if len(userSession.PendingBuys) <= 1 {
		// clear the list
		userSession.PendingBuys = nil
	} else {
		// Pop last sell off the pending list.
		userSession.PendingBuys = userSession.PendingBuys[1:]
	}
}

func (webServer *WebServer) sellHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")
	amount := request.FormValue("amount")
	command := commands.NewCommand("SELL", username, []string{stock, amount})

	webServer.logger.UserCommand(webServer.Name, currTransNum, "SELL",
		username, stock, nil, amount)

	val, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}
	userSession := val.(*usersessions.UserSession)

	resp := webServer.transmitter.MakeRequest(currTransNum, "SELL,"+username+","+stock+","+amount)
	if resp == "-1" {
		http.Error(writer, "Bad response from transactionserv", 400)
		return
	}

	userSession.PendingSells = append(userSession.PendingSells, command)
}

func (webServer *WebServer) commitSellHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "COMMIT_SELL",
		username, nil, nil, nil)

	val, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}
	userSession := val.(*usersessions.UserSession)

	if !userSession.HasPendingSells() {
		// No pendings buys, return error
		webServer.logger.SystemError(webServer.Name, currTransNum, "COMMIT_SELL",
			username, nil, nil, nil, "No pending sells to commit")
		http.Error(writer, "No pending sells to commit", 400)
		return
	}

	command := userSession.PendingSells[0]
	var resp string

	if command.HasTimeElapsed() {
		// Time has elapsed on Buy, automatically cancel request
		resp = webServer.transmitter.MakeRequest(currTransNum, "CANCEL_SELL,"+username)
		webServer.logger.SystemError(webServer.Name, currTransNum, "COMMIT_SELL",
			username, nil, nil, nil, "Time elapsed on most recent sell")
		http.Error(writer, "Time elapsed on most recent sell", 400)
		// Pop off request when invalid
		if len(userSession.PendingSells) <= 1 {
			// clear the list
			userSession.PendingSells = nil
		} else {
			// Pop last sell off the pending list.
			userSession.PendingSells = userSession.PendingSells[1:]
		}
		return
	} else {
		resp = webServer.transmitter.MakeRequest(currTransNum, "COMMIT_SELL,"+username)
	}

	if resp == "-1" {
		http.Error(writer, "Invalid request", 400)
		return
	}
	if len(userSession.PendingSells) <= 1 {
		// clear the list
		userSession.PendingSells = nil
	} else {
		// Pop last sell off the pending list.
		userSession.PendingSells = userSession.PendingSells[1:]
	}

}

func (webServer *WebServer) cancelSellHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	webServer.logger.UserCommand(webServer.Name, currTransNum, "CANCEL_SELL",
		username, nil, nil, nil)

	val, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}
	userSession := val.(*usersessions.UserSession)

	if !userSession.HasPendingSells() {
		webServer.logger.SystemError(webServer.Name, currTransNum, "CANCEL_SELL",
			username, nil, nil, nil, "User has no pending sells")
		http.Error(writer, "No pending sells to cancel", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "CANCEL_SELL,"+username)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}

	if len(userSession.PendingSells) <= 1 {
		// clear the list
		userSession.PendingSells = nil
	} else {
		// Pop last sell off the pending list.
		userSession.PendingSells = userSession.PendingSells[1:]
	}
}

func (webServer *WebServer) setBuyAmountHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")
	amount := request.FormValue("amount")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "SET_BUY_AMOUNT",
		username, stock, nil, amount)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "SET_BUY_AMOUNT,"+username+","+stock+","+amount)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) cancelSetBuyHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "CANCEL_SET_BUY",
		username, stock, nil, nil)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "CANCEL_SET_BUY,"+username+","+stock)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) setBuyTriggerHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")
	amount := request.FormValue("amount")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "SET_BUY_TRIGGER",
		username, stock, nil, amount)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "SET_BUY_TRIGGER,"+username+","+stock+","+amount)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) setSellAmountHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")
	amount := request.FormValue("amount")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "SET_SELL_AMOUNT",
		username, stock, nil, amount)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "SET_SELL_AMOUNT,"+username+","+stock+","+amount)

	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) setSellTriggerHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")
	amount := request.FormValue("amount")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "SET_SELL_TRIGGER",
		username, stock, nil, amount)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "SET_SELL_TRIGGER,"+username+","+stock+","+amount)
	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) cancelSetSellHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	stock := request.FormValue("stock")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "CANCEL_SET_SELL",
		username, stock, nil, nil)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "CANCEL_SET_SELL,"+username+","+stock)
	if resp == "-1" {
		http.Error(writer, "Invalid Request", 400)
		return
	}
}

func (webServer *WebServer) dumplogHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")
	filename := request.FormValue("filename")

	if len(username) == 0 {
		webServer.logger.UserCommand(webServer.Name, currTransNum, "DUMPLOG",
			nil, nil, filename, nil)
	} else {
		webServer.logger.UserCommand(webServer.Name, currTransNum, "DUMPLOG",
			username, nil, filename, nil)
	}

	webServer.logger.DumpLog(filename, nil)
	file := webServer.transmitter.RetrieveDumplog(filename)
	writer.Write(file)
}

func (webServer *WebServer) displaySummaryHandler(writer http.ResponseWriter, request *http.Request) {
	currTransNum := int(atomic.AddInt64(&webServer.transactionNumber, 1))
	username := request.FormValue("username")

	webServer.logger.UserCommand(webServer.Name, currTransNum, "DISPLAY_SUMMARY",
		username, nil, nil, nil)

	_, ok := webServer.userSessions.Load(username)
	// User must be logged in to execute any commands.
	if !ok {
		http.Error(writer, "Must be logged in to perform commands", 400)
		return
	}

	resp := webServer.transmitter.MakeRequest(currTransNum, "DISPLAY_SUMMARY,"+username)
	if resp == "-1" {
		webServer.logger.SystemError(webServer.Name, currTransNum, "DISPLAY_SUMMARY",
			username, nil, nil, nil, "Bad response from transactionserv")
		http.Error(writer, "Invalid Request", 400)
		return
	}
	lines := strings.Split(resp, ";")
	fmt.Fprintln(writer, strings.Join(lines, "\n"))
}

func main() {
	serverAddress := ":" + os.Getenv("webport")
	auditAddr := "http://" + os.Getenv("auditaddr") + ":" + os.Getenv("auditport")

	webServer := &WebServer{
		Name:              "webserver",
		transactionNumber: 0,
		userSessions:      new(syncmap.Map),
		transmitter:       transmitter.NewTransmitter(os.Getenv("transaddr"), os.Getenv("transport")),
		logger: logger.AuditLogger{
			Addr: auditAddr,
			Client: http.Client{
				Timeout: time.Second,
			},
		},
		validPath: regexp.MustCompile("^/(ADD|QUOTE|BUY|COMMIT_BUY|CANCEL_BUY|SELL|COMMIT_SELL|CANCEL_SELL|SET_BUY_AMOUNT|CANCEL_SET_BUY|SET_BUY_TRIGGER|SET_SELL_AMOUNT|SET_SELL_TRIGGER|CANCEL_SET_SELL|DUMPLOG|DISPLAY_SUMMARY|LOGIN)/$"),
	}

	http.Handle("/", http.FileServer(http.Dir("./html")))
	http.HandleFunc("/ADD/", webServer.addHandler)
	http.HandleFunc("/QUOTE/", webServer.quoteHandler)
	http.HandleFunc("/BUY/", webServer.buyHandler)
	http.HandleFunc("/COMMIT_BUY/", webServer.commitBuyHandler)
	http.HandleFunc("/CANCEL_BUY/", webServer.cancelBuyHandler)
	http.HandleFunc("/SELL/", webServer.sellHandler)
	http.HandleFunc("/COMMIT_SELL/", webServer.commitSellHandler)
	http.HandleFunc("/CANCEL_SELL/", webServer.cancelSellHandler)
	http.HandleFunc("/SET_BUY_AMOUNT/", webServer.setBuyAmountHandler)
	http.HandleFunc("/CANCEL_SET_BUY/", webServer.cancelSetBuyHandler)
	http.HandleFunc("/SET_BUY_TRIGGER/", webServer.setBuyTriggerHandler)
	http.HandleFunc("/SET_SELL_AMOUNT/", webServer.setSellAmountHandler)
	http.HandleFunc("/SET_SELL_TRIGGER/", webServer.setSellTriggerHandler)
	http.HandleFunc("/CANCEL_SET_SELL/", webServer.cancelSetSellHandler)
	http.HandleFunc("/DUMPLOG/", webServer.dumplogHandler)
	http.HandleFunc("/DISPLAY_SUMMARY/", webServer.displaySummaryHandler)
	http.HandleFunc("/LOGIN/", webServer.loginHandler)

	fmt.Printf("Successfully started server on %s\n", serverAddress)
	panic(http.ListenAndServe(":"+os.Getenv("webport"), nil))
}
