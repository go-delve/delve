package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
)

// Client is a REST service.Client.
type RESTClient struct {
	addr       string
	httpClient *http.Client
}

// ClientError is an error from the debugger.
type ClientError struct {
	// Message is the specific error from the debugger.
	Message string
	// Status is the HTTP error from the debugger request.
	Status string
}

func (e *ClientError) Error() string { return e.Message }

// Ensure the implementation satisfies the interface.
var _ service.Client = &RESTClient{}

// NewClient creates a new RESTClient.
func NewClient(addr string) *RESTClient {
	return &RESTClient{
		addr:       addr,
		httpClient: &http.Client{},
	}
}

func (c *RESTClient) Detach(killProcess bool) error {
	params := [][]string{{"kill", strconv.FormatBool(killProcess)}}
	err := c.doGET("/detach", nil, params...)
	if err != nil {
		return err
	}
	return nil
}

func (c *RESTClient) GetState() (*api.DebuggerState, error) {
	var state *api.DebuggerState
	err := c.doGET("/state", &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *RESTClient) Continue() (*api.DebuggerState, error) {
	var state *api.DebuggerState
	err := c.doPOST("/command", &api.DebuggerCommand{Name: api.Continue}, &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *RESTClient) Next() (*api.DebuggerState, error) {
	var state *api.DebuggerState
	err := c.doPOST("/command", &api.DebuggerCommand{Name: api.Next}, &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *RESTClient) Step() (*api.DebuggerState, error) {
	var state *api.DebuggerState
	err := c.doPOST("/command", &api.DebuggerCommand{Name: api.Step}, &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *RESTClient) SwitchThread(threadID int) (*api.DebuggerState, error) {
	var state *api.DebuggerState
	err := c.doPOST("/command", &api.DebuggerCommand{
		Name:     api.SwitchThread,
		ThreadID: threadID,
	}, &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *RESTClient) Halt() (*api.DebuggerState, error) {
	var state *api.DebuggerState
	err := c.doPOST("/command", &api.DebuggerCommand{Name: api.Halt}, &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *RESTClient) GetBreakpoint(id int) (*api.Breakpoint, error) {
	var breakPoint *api.Breakpoint
	err := c.doGET(fmt.Sprintf("/breakpoints/%d", id), &breakPoint)
	if err != nil {
		return nil, err
	}
	return breakPoint, nil
}

func (c *RESTClient) CreateBreakpoint(breakPoint *api.Breakpoint) (*api.Breakpoint, error) {
	var newBreakpoint *api.Breakpoint
	err := c.doPOST("/breakpoints", breakPoint, &newBreakpoint)
	if err != nil {
		return nil, err
	}
	return newBreakpoint, nil
}

func (c *RESTClient) ListBreakpoints() ([]*api.Breakpoint, error) {
	var breakPoints []*api.Breakpoint
	err := c.doGET("/breakpoints", &breakPoints)
	if err != nil {
		return nil, err
	}
	return breakPoints, nil
}

func (c *RESTClient) ClearBreakpoint(id int) (*api.Breakpoint, error) {
	var breakPoint *api.Breakpoint
	err := c.doDELETE(fmt.Sprintf("/breakpoints/%d", id), &breakPoint)
	if err != nil {
		return nil, err
	}
	return breakPoint, nil
}

func (c *RESTClient) ListThreads() ([]*api.Thread, error) {
	var threads []*api.Thread
	err := c.doGET("/threads", &threads)
	if err != nil {
		return nil, err
	}
	return threads, nil
}

func (c *RESTClient) GetThread(id int) (*api.Thread, error) {
	var thread *api.Thread
	err := c.doGET(fmt.Sprintf("/threads/%d", id), &thread)
	if err != nil {
		return nil, err
	}
	return thread, nil
}

func (c *RESTClient) EvalVariable(symbol string) (*api.Variable, error) {
	var v *api.Variable
	err := c.doGET(fmt.Sprintf("/eval/%s", symbol), &v)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (c *RESTClient) EvalVariableFor(threadID int, symbol string) (*api.Variable, error) {
	var v *api.Variable
	err := c.doGET(fmt.Sprintf("/threads/%d/eval/%s", threadID, symbol), &v)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (c *RESTClient) ListSources(filter string) ([]string, error) {
	params := [][]string{}
	if len(filter) > 0 {
		params = append(params, []string{"filter", filter})
	}
	var sources []string
	err := c.doGET("/sources", &sources, params...)
	if err != nil {
		return nil, err
	}
	return sources, nil
}

func (c *RESTClient) ListFunctions(filter string) ([]string, error) {
	params := [][]string{}
	if len(filter) > 0 {
		params = append(params, []string{"filter", filter})
	}
	var funcs []string
	err := c.doGET("/functions", &funcs, params...)
	if err != nil {
		return nil, err
	}
	return funcs, nil
}

func (c *RESTClient) ListPackageVariables(filter string) ([]api.Variable, error) {
	params := [][]string{}
	if len(filter) > 0 {
		params = append(params, []string{"filter", filter})
	}
	var vars []api.Variable
	err := c.doGET(fmt.Sprintf("/vars"), &vars, params...)
	if err != nil {
		return nil, err
	}
	return vars, nil
}

func (c *RESTClient) ListPackageVariablesFor(threadID int, filter string) ([]api.Variable, error) {
	params := [][]string{}
	if len(filter) > 0 {
		params = append(params, []string{"filter", filter})
	}
	var vars []api.Variable
	err := c.doGET(fmt.Sprintf("/threads/%d/vars", threadID), &vars, params...)
	if err != nil {
		return nil, err
	}
	return vars, nil
}

func (c *RESTClient) ListLocalVariables() ([]api.Variable, error) {
	var vars []api.Variable
	err := c.doGET("/localvars", &vars)
	if err != nil {
		return nil, err
	}
	return vars, nil
}

func (c *RESTClient) ListRegisters() (string, error) {
	var regs string
	err := c.doGET("/regs", &regs)
	if err != nil {
		return "", err
	}
	return regs, nil
}

func (c *RESTClient) ListFunctionArgs() ([]api.Variable, error) {
	var vars []api.Variable
	err := c.doGET("/args", &vars)
	if err != nil {
		return nil, err
	}
	return vars, nil
}

func (c *RESTClient) ListGoroutines() ([]*api.Goroutine, error) {
	var goroutines []*api.Goroutine
	err := c.doGET("/goroutines", &goroutines)
	if err != nil {
		return nil, err
	}
	return goroutines, nil
}

func (c *RESTClient) Stacktrace(goroutineId, depth int) ([]*api.Location, error) {
	var locations []*api.Location
	err := c.doGET(fmt.Sprintf("/goroutines/%d/trace?depth=%d", goroutineId, depth), &locations)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// TODO: how do we use http.Client with a UNIX socket URI?
func (c *RESTClient) url(path string) string {
	return fmt.Sprintf("http://%s%s", c.addr, path)
}

// doGET performs an HTTP GET to path and stores the resulting API object in
// obj. Query parameters are passed as an array of 2-element string arrays
// representing key-value pairs.
func (c *RESTClient) doGET(path string, obj interface{}, params ...[]string) error {
	url, err := url.Parse(c.url(path))
	if err != nil {
		return err
	}

	// Add any supplied query parameters to the URL
	q := url.Query()
	for _, p := range params {
		q.Set(p[0], p[1])
	}
	url.RawQuery = q.Encode()

	// Create the request
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	// Execute the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Extract error text and return
	if resp.StatusCode != http.StatusOK {
		contents, _ := ioutil.ReadAll(resp.Body)
		return &ClientError{Message: string(contents), Status: resp.Status}
	}

	// Decode result object
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&obj)
	if err != nil {
		return err
	}
	return nil
}

// doPOST performs an HTTP POST to path, sending 'out' as the body and storing
// the resulting API object to 'in'.
func (c *RESTClient) doPOST(path string, out interface{}, in interface{}) error {
	jsonString, err := json.Marshal(out)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.url(path), bytes.NewBuffer(jsonString))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		contents, _ := ioutil.ReadAll(resp.Body)
		return &ClientError{Message: string(contents), Status: resp.Status}
	}

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&in)
	if err != nil {
		return err
	}
	return nil
}

// doDELETE performs an HTTP DELETE to path, storing the resulting API object
// to 'obj'.
func (c *RESTClient) doDELETE(path string, obj interface{}) error {
	req, err := http.NewRequest("DELETE", c.url(path), nil)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		contents, _ := ioutil.ReadAll(resp.Body)
		return &ClientError{Message: string(contents), Status: resp.Status}
	}

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&obj)
	if err != nil {
		return err
	}
	return nil
}

// doPUT performs an HTTP PUT to path, sending 'out' as the body and storing
// the resulting API object to 'in'.
func (c *RESTClient) doPUT(path string, out interface{}, in interface{}) error {
	jsonString, err := json.Marshal(out)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.url(path), bytes.NewBuffer(jsonString))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		contents, _ := ioutil.ReadAll(resp.Body)
		return &ClientError{Message: string(contents), Status: resp.Status}
	}

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&in)
	if err != nil {
		return err
	}
	return nil
}
