package rancher_client

import (
  "context"
	"bytes"
	"crypto/x509"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"
  "encoding/json"
  "io/ioutil"

  "github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ RancherClient = &RancherHttpClient{} // make sure the RancherHttpClient implements the RancherClient
var _ RancherRequest = &RancherHttpRequest{} // make sure the RancherHttpRequest implements the RancherRequest

type RancherHttpClient struct{
  ApiURL         string
  CACert         string
  IgnoreSystemCA bool
  Insecure       bool
  Token          string
  MaxRedirects   int
  Timeout        time.Duration
}

func NewRancherHttpClient(apiURL string, caCert string, ignoreSystemCA bool, insecure bool, token string, maxRedirects int, timeout time.Duration) *RancherHttpClient {
  return &RancherHttpClient{
    ApiURL:         apiURL,
    CACert:         caCert,
    IgnoreSystemCA: ignoreSystemCA,
    Insecure:       insecure,
    Token:          token,
    MaxRedirects:   maxRedirects,
    Timeout:       timeout,
  }
}

type RancherHttpRequest struct {
  Method       string
  Endpoint     string
  Body         interface{}
  Headers      map[string]string
}

func (c *RancherHttpClient) Create(ctx context.Context, r RancherRequest) error {
  _, err := r.DoRequest(ctx, c)
  return err
}

func (c *RancherHttpClient) Read(ctx context.Context, r RancherRequest) ([]byte, error) {
  return r.DoRequest(ctx, c)
}

func (c *RancherHttpClient) Update(ctx context.Context, r RancherRequest) error {
  _, err := r.DoRequest(ctx, c)
  return err
}

func (c *RancherHttpClient) Delete(ctx context.Context, r RancherRequest) error {
  _, err := r.DoRequest(ctx, c)
  return err
}


func (r *RancherHttpRequest) DoRequest(ctx context.Context, rc RancherClient) ([]byte, error) {
  start := time.Now()

  c, ok := rc.(*RancherHttpClient)
  if !ok {
    tflog.Error(ctx, "Doing request: invalid rancher client type")
    return nil, fmt.Errorf("Doing request: invalid rancher client type")
  }

  if r.Endpoint == "" {
    tflog.Error(ctx, "Doing request: URL is nil")
    return nil, fmt.Errorf("Doing request: URL is nil")
  }

  tflog.Debug(ctx, fmt.Sprintf("Request Object: %#v", r))

  MaxRedirectCheckFunction := func(req *http.Request, via []*http.Request) error {
    if len(via) >= c.MaxRedirects {
      tflog.Error(ctx, fmt.Sprintf("Stopped after %d redirects", c.MaxRedirects))
      return fmt.Errorf("Stopped after %d redirects", c.MaxRedirects)
    }
    if len(c.Token) > 0 {
      // make sure the auth token is added to redirected requests
      req.Header.Add("Authorization", "Bearer "+c.Token)
    }
    return nil
  }

  var rootCAs *x509.CertPool
  if c.IgnoreSystemCA {
    rootCAs = x509.NewCertPool()
  } else {
    // Get the SystemCertPool, continue with an empty pool on error
    rootCAs, _ = x509.SystemCertPool()
    if rootCAs == nil {
      rootCAs = x509.NewCertPool()
    }
  }

  if c.CACert != "" {
    // Append our cert to the cert pool
    if ok := rootCAs.AppendCertsFromPEM([]byte(c.CACert)); !ok {
      tflog.Warn(ctx, "No certs appended, using system certs only")
    }
  }

  tlsConfig := &tls.Config{
    InsecureSkipVerify: c.Insecure,
    RootCAs: rootCAs,
  }

  transport := &http.Transport{
    TLSClientConfig: tlsConfig,
    Proxy:           http.ProxyFromEnvironment,
  }
  
  client := &http.Client{
    Timeout: c.Timeout,
    CheckRedirect: MaxRedirectCheckFunction,
    Transport: transport,
  }

  var reqBody *bytes.Buffer
  if r.Body != nil {
    bodyBytes, err := json.Marshal(r.Body)
    if err != nil {
      tflog.Error(ctx, fmt.Sprintf("Doing request: error marshalling body: %v", err))
      return nil, fmt.Errorf("Doing request: error marshalling body: %v", err)
    }
    reqBody = bytes.NewBuffer(bodyBytes)
  } else {
    reqBody = &bytes.Buffer{}
  }
  
  request, err := http.NewRequest(r.Method, r.Endpoint, reqBody)
  if err != nil {
    tflog.Error(ctx, fmt.Sprintf("Doing request: %v", err))
    return nil, fmt.Errorf("Doing request: %v", err)
  }
  
  for key, value := range r.Headers {
    request.Header.Add(key, value)
  }
  
  if len(c.Token) > 0 {
    request.Header.Add("Authorization", "Bearer "+c.Token)
  }
  
  resp, err := client.Do(request)
  if err != nil {
    tflog.Error(ctx, fmt.Sprintf("Doing request: %v", err))
    return nil, fmt.Errorf("Doing request: %v", err)
  }
  defer resp.Body.Close()
  
  // Timings recorded as part of internal metrics
  tflog.Debug(ctx, fmt.Sprintf("Response Time: %f ms", float64((time.Since(start))/time.Millisecond)))
  tflog.Debug(ctx, fmt.Sprintf("Response Status: %s", resp.Status))
  tflog.Debug(ctx, fmt.Sprintf("Response Headers: %#v", resp.Header))
  tflog.Debug(ctx, fmt.Sprintf("Response Body: %v", resp.Body))
  
  return ioutil.ReadAll(resp.Body)
}
