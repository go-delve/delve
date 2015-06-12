package rest

import (
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"

	restful "github.com/emicklei/go-restful"

	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/debugger"
)

// RESTServer exposes a Debugger via a HTTP REST API.
type RESTServer struct {
	// config is all the information necessary to start the debugger and server.
	config *Config
	// listener is used to serve HTTP.
	listener net.Listener
	// debugger is a debugger service.
	debugger *debugger.Debugger
	// debuggerStopped is used to detect shutdown of the debugger service.
	debuggerStopped chan error
}

// Config provides the configuration to start a Debugger and expose it with a
// RESTServer.
//
// Only one of ProcessArgs or AttachPid should be specified. If ProcessArgs is
// provided, a new process will be launched. Otherwise, the debugger will try
// to attach to an existing process with AttachPid.
type Config struct {
	// Listener is used to serve HTTP.
	Listener net.Listener
	// ProcessArgs are the arguments to launch a new process.
	ProcessArgs []string
	// AttachPid is the PID of an existing process to which the debugger should
	// attach.
	AttachPid int
}

// NewServer creates a new RESTServer.
func NewServer(config *Config, logEnabled bool) *RESTServer {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if !logEnabled {
		log.SetOutput(ioutil.Discard)
	}

	return &RESTServer{
		config:          config,
		listener:        config.Listener,
		debuggerStopped: make(chan error),
	}
}

// Run starts a debugger and exposes it with an HTTP server. The debugger
// itself can be stopped with the `detach` API. Run blocks until the HTTP
// server stops.
func (s *RESTServer) Run() error {
	// Create and start the debugger
	s.debugger = debugger.New(&debugger.Config{
		ProcessArgs: s.config.ProcessArgs,
		AttachPid:   s.config.AttachPid,
	})
	go func() {
		err := s.debugger.Run()
		if err != nil {
			log.Printf("debugger stopped with error: %s", err)
		}
		s.debuggerStopped <- err
	}()

	// Set up the HTTP server
	container := restful.NewContainer()

	ws := new(restful.WebService)
	ws.
		Path("").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON).
		Route(ws.GET("/state").To(s.getState)).
		Route(ws.GET("/breakpoints").To(s.listBreakPoints)).
		Route(ws.GET("/breakpoints/{breakpoint-id}").To(s.getBreakPoint)).
		Route(ws.POST("/breakpoints").To(s.createBreakPoint)).
		Route(ws.DELETE("/breakpoints/{breakpoint-id}").To(s.clearBreakPoint)).
		Route(ws.GET("/threads").To(s.listThreads)).
		Route(ws.GET("/threads/{thread-id}").To(s.getThread)).
		Route(ws.GET("/threads/{thread-id}/vars").To(s.listThreadPackageVars)).
		Route(ws.GET("/threads/{thread-id}/eval/{symbol}").To(s.evalThreadSymbol)).
		Route(ws.GET("/goroutines").To(s.listGoroutines)).
		Route(ws.POST("/command").To(s.doCommand)).
		Route(ws.GET("/sources").To(s.listSources)).
		Route(ws.GET("/source").To(s.listSource)).
		Route(ws.GET("/functions").To(s.listFunctions)).
		Route(ws.GET("/vars").To(s.listPackageVars)).
		Route(ws.GET("/localvars").To(s.listLocalVars)).
		Route(ws.GET("/args").To(s.listFunctionArgs)).
		Route(ws.GET("/eval/{symbol}").To(s.evalSymbol)).
		// TODO: GET might be the wrong verb for this
		Route(ws.GET("/detach").To(s.detach))
	container.Add(ws)

	// Start the HTTP server
	log.Printf("server listening on %s", s.listener.Addr())
	return http.Serve(s.listener, container)
}

// Stop detaches from the debugger and waits for it to stop.
func (s *RESTServer) Stop(kill bool) error {
	err := s.debugger.Detach(kill)
	if err != nil {
		return err
	}
	return <-s.debuggerStopped
}

// writeError writes a simple error response.
func writeError(response *restful.Response, statusCode int, message string) {
	response.AddHeader("Content-Type", "text/plain")
	response.WriteErrorString(statusCode, message)
}

// detach stops the debugger and waits for it to shut down before returning an
// OK response. Clients expect this to be a synchronous call.
func (s *RESTServer) detach(request *restful.Request, response *restful.Response) {
	kill, err := strconv.ParseBool(request.QueryParameter("kill"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid kill parameter")
		return
	}

	err = s.Stop(kill)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
}

func (s *RESTServer) getState(request *restful.Request, response *restful.Response) {
	state, err := s.debugger.State()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteEntity(state)
}

func (s *RESTServer) doCommand(request *restful.Request, response *restful.Response) {
	command := new(api.DebuggerCommand)
	err := request.ReadEntity(command)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	state, err := s.debugger.Command(command)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusCreated)
	response.WriteEntity(state)
}

func (s *RESTServer) getBreakPoint(request *restful.Request, response *restful.Response) {
	id, err := strconv.Atoi(request.PathParameter("breakpoint-id"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid breakpoint id")
		return
	}

	found := s.debugger.FindBreakPoint(id)
	if found == nil {
		writeError(response, http.StatusNotFound, "breakpoint not found")
		return
	}
	response.WriteHeader(http.StatusOK)
	response.WriteEntity(found)
}

func (s *RESTServer) listBreakPoints(request *restful.Request, response *restful.Response) {
	response.WriteEntity(s.debugger.BreakPoints())
}

func (s *RESTServer) createBreakPoint(request *restful.Request, response *restful.Response) {
	location := new(api.Arguments)
	err := request.ReadEntity(location)
	createdbp, err := s.debugger.CreateBreakPoint(location)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusCreated)
	response.WriteEntity(createdbp)
}

func (s *RESTServer) clearBreakPoint(request *restful.Request, response *restful.Response) {
	id, err := strconv.Atoi(request.PathParameter("breakpoint-id"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid breakpoint id")
		return
	}

	found := s.debugger.FindBreakPoint(id)
	if found == nil {
		writeError(response, http.StatusNotFound, "breakpoint not found")
		return
	}

	deleted, err := s.debugger.ClearBreakPoint(found)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusOK)
	response.WriteEntity(deleted)
}

func (s *RESTServer) listThreads(request *restful.Request, response *restful.Response) {
	response.WriteEntity(s.debugger.Threads())
}

func (s *RESTServer) getThread(request *restful.Request, response *restful.Response) {
	id, err := strconv.Atoi(request.PathParameter("thread-id"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid thread id")
		return
	}

	found := s.debugger.FindThread(id)
	if found == nil {
		writeError(response, http.StatusNotFound, "thread not found")
		return
	}
	response.WriteHeader(http.StatusOK)
	response.WriteEntity(found)
}

func (s *RESTServer) listPackageVars(request *restful.Request, response *restful.Response) {
	state, err := s.debugger.State()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	current := state.CurrentThread
	if current == nil {
		writeError(response, http.StatusBadRequest, "no current thread")
		return
	}

	filter := request.QueryParameter("filter")
	vars, err := s.debugger.PackageVariables(current.ID, filter)

	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(vars)
}

func (s *RESTServer) listThreadPackageVars(request *restful.Request, response *restful.Response) {
	id, err := strconv.Atoi(request.PathParameter("thread-id"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid thread id")
		return
	}

	if found := s.debugger.FindThread(id); found == nil {
		writeError(response, http.StatusNotFound, "thread not found")
		return
	}

	filter := request.QueryParameter("filter")
	vars, err := s.debugger.PackageVariables(id, filter)

	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(vars)
}

func (s *RESTServer) listLocalVars(request *restful.Request, response *restful.Response) {
	state, err := s.debugger.State()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	vars, err := s.debugger.LocalVariables(state.CurrentThread.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(vars)
}

func (s *RESTServer) listFunctionArgs(request *restful.Request, response *restful.Response) {
	state, err := s.debugger.State()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	vars, err := s.debugger.FunctionArguments(state.CurrentThread.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(vars)
}

func (s *RESTServer) evalSymbol(request *restful.Request, response *restful.Response) {
	symbol := request.PathParameter("symbol")
	if len(symbol) == 0 {
		writeError(response, http.StatusBadRequest, "invalid symbol")
		return
	}

	state, err := s.debugger.State()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	current := state.CurrentThread
	if current == nil {
		writeError(response, http.StatusBadRequest, "no current thread")
		return
	}

	v, err := s.debugger.EvalVariableInThread(current.ID, symbol)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(v)
}

func (s *RESTServer) evalThreadSymbol(request *restful.Request, response *restful.Response) {
	id, err := strconv.Atoi(request.PathParameter("thread-id"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid thread id")
		return
	}

	if found := s.debugger.FindThread(id); found == nil {
		writeError(response, http.StatusNotFound, "thread not found")
		return
	}

	symbol := request.PathParameter("symbol")
	if len(symbol) == 0 {
		writeError(response, http.StatusNotFound, "invalid symbol")
		return
	}

	v, err := s.debugger.EvalVariableInThread(id, symbol)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(v)
}

func (s *RESTServer) listSource(request *restful.Request, response *restful.Response) {
	location := request.QueryParameter("location")
	sources, err := s.debugger.ListSource(location)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(sources)
}

func (s *RESTServer) listSources(request *restful.Request, response *restful.Response) {
	filter := request.QueryParameter("filter")
	sources, err := s.debugger.Sources(filter)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(sources)
}

func (s *RESTServer) listFunctions(request *restful.Request, response *restful.Response) {
	filter := request.QueryParameter("filter")
	funcs, err := s.debugger.Functions(filter)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(funcs)
}

func (s *RESTServer) listGoroutines(request *restful.Request, response *restful.Response) {
	gs, err := s.debugger.Goroutines()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	response.WriteHeader(http.StatusOK)
	response.WriteEntity(gs)
}
