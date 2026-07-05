package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// This file generates the per-endpoint x-codeSamples (curl / Go / Python / Java)
// from the same catalog that builds the OpenAPI paths, so the in-page language
// switcher always matches the real routes.

const sampleTokenPlaceholder = "YOUR_API_TOKEN"

// sampleURL renders an endpoint path with its example path/query values filled
// in, prefixed by the docs base URL.
func sampleURL(ep apiEndpoint) string {
	path := ep.Path
	var query []string
	for _, p := range ep.Params {
		switch p.In {
		case "path":
			val := p.Example
			if val == "" {
				val = ":" + p.Name
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", val)
		case "query":
			if p.Example != "" {
				query = append(query, p.Name+"="+url.QueryEscape(p.Example))
			}
		}
	}
	u := docsBaseURL + path
	if len(query) > 0 {
		u += "?" + strings.Join(query, "&")
	}
	return u
}

// sampleBody returns the request body as a string (indented JSON or raw YAML),
// and whether the endpoint has a body at all.
func sampleBody(ep apiEndpoint) (string, bool) {
	if ep.Request == nil || ep.Method == http.MethodGet {
		return "", false
	}
	if ep.RequestType == "yaml" {
		if s, ok := ep.Request.(string); ok {
			return s, true
		}
	}
	b, err := json.MarshalIndent(ep.Request, "", "  ")
	if err != nil {
		return "", false
	}
	return string(b), true
}

func sampleContentType(ep apiEndpoint) string {
	if ep.RequestType == "yaml" {
		return "application/yaml"
	}
	return "application/json"
}

// codeSamples produces the x-codeSamples array Redoc renders as switchable tabs.
func codeSamples(ep apiEndpoint) []any {
	return []any{
		map[string]any{"lang": "bash", "label": "cURL", "source": sampleCurl(ep)},
		map[string]any{"lang": "go", "label": "Go", "source": sampleGo(ep)},
		map[string]any{"lang": "python", "label": "Python", "source": samplePython(ep)},
		map[string]any{"lang": "java", "label": "Java", "source": sampleJava(ep)},
	}
}

func sampleCurl(ep apiEndpoint) string {
	u := sampleURL(ep)
	body, hasBody := sampleBody(ep)
	var lines []string
	first := "curl -X " + ep.Method + " \"" + u + "\""
	if ep.Method == http.MethodGet && !hasBody && ep.NoAuth {
		return "curl \"" + u + "\""
	}
	lines = append(lines, first)
	if !ep.NoAuth {
		lines = append(lines, "-H \"Authorization: Bearer $CRONOVA_TOKEN\"")
	}
	if hasBody {
		lines = append(lines, "-H \"Content-Type: "+sampleContentType(ep)+"\"")
		lines = append(lines, "-d '"+body+"'")
	}
	return strings.Join(lines, " \\\n  ")
}

func sampleGo(ep apiEndpoint) string {
	u := sampleURL(ep)
	body, hasBody := sampleBody(ep)
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n")
	if hasBody {
		b.WriteString("\t\"bytes\"\n")
	}
	b.WriteString("\t\"fmt\"\n\t\"io\"\n\t\"net/http\"\n)\n\n")
	b.WriteString("func main() {\n")
	if !ep.NoAuth {
		b.WriteString("\ttoken := \"" + sampleTokenPlaceholder + "\"\n")
	}
	if hasBody {
		b.WriteString("\tbody := bytes.NewBufferString(`" + body + "`)\n")
		b.WriteString("\treq, _ := http.NewRequest(\"" + ep.Method + "\", \"" + u + "\", body)\n")
	} else {
		b.WriteString("\treq, _ := http.NewRequest(\"" + ep.Method + "\", \"" + u + "\", nil)\n")
	}
	if !ep.NoAuth {
		b.WriteString("\treq.Header.Set(\"Authorization\", \"Bearer \"+token)\n")
	}
	if hasBody {
		b.WriteString("\treq.Header.Set(\"Content-Type\", \"" + sampleContentType(ep) + "\")\n")
	}
	b.WriteString("\tresp, err := http.DefaultClient.Do(req)\n")
	b.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	b.WriteString("\tdefer resp.Body.Close()\n")
	b.WriteString("\tout, _ := io.ReadAll(resp.Body)\n")
	b.WriteString("\tfmt.Println(resp.Status, string(out))\n")
	b.WriteString("}\n")
	return b.String()
}

func samplePython(ep apiEndpoint) string {
	u := sampleURL(ep)
	body, hasBody := sampleBody(ep)
	method := strings.ToLower(ep.Method)
	var b strings.Builder
	b.WriteString("import requests\n\n")
	if !ep.NoAuth {
		b.WriteString("token = \"" + sampleTokenPlaceholder + "\"\n")
	}
	// Build headers dict.
	var headers []string
	if !ep.NoAuth {
		headers = append(headers, "\"Authorization\": f\"Bearer {token}\"")
	}
	if hasBody {
		headers = append(headers, "\"Content-Type\": \""+sampleContentType(ep)+"\"")
	}
	if len(headers) > 0 {
		b.WriteString("headers = {" + strings.Join(headers, ", ") + "}\n")
	}
	if hasBody {
		b.WriteString("payload = \"\"\"" + body + "\"\"\"\n\n")
	} else {
		b.WriteString("\n")
	}
	call := "resp = requests." + method + "(\n    \"" + u + "\""
	if len(headers) > 0 {
		call += ",\n    headers=headers"
	}
	if hasBody {
		call += ",\n    data=payload"
	}
	call += ",\n)\n"
	b.WriteString(call)
	b.WriteString("print(resp.status_code, resp.text)\n")
	return b.String()
}

func sampleJava(ep apiEndpoint) string {
	u := sampleURL(ep)
	body, hasBody := sampleBody(ep)
	var b strings.Builder
	b.WriteString("import java.net.URI;\n")
	b.WriteString("import java.net.http.HttpClient;\n")
	b.WriteString("import java.net.http.HttpRequest;\n")
	b.WriteString("import java.net.http.HttpResponse;\n\n")
	b.WriteString("// Requires Java 17+ (HttpClient + text blocks).\n")
	b.WriteString("var client = HttpClient.newHttpClient();\n")
	b.WriteString("var request = HttpRequest.newBuilder()\n")
	b.WriteString("    .uri(URI.create(\"" + u + "\"))\n")
	if !ep.NoAuth {
		b.WriteString("    .header(\"Authorization\", \"Bearer " + sampleTokenPlaceholder + "\")\n")
	}
	if hasBody {
		b.WriteString("    .header(\"Content-Type\", \"" + sampleContentType(ep) + "\")\n")
		// Indent the body inside a Java text block.
		indented := strings.ReplaceAll(body, "\n", "\n        ")
		b.WriteString("    ." + javaVerb(ep.Method) + "(HttpRequest.BodyPublishers.ofString(\"\"\"\n        " + indented + "\"\"\"))\n")
	} else {
		b.WriteString("    ." + javaBodylessCall(ep.Method) + "\n")
	}
	b.WriteString("    .build();\n")
	b.WriteString("var response = client.send(request, HttpResponse.BodyHandlers.ofString());\n")
	b.WriteString("System.out.println(response.statusCode() + \" \" + response.body());\n")
	return b.String()
}

// javaVerb is the HttpRequest.Builder verb for the body-bearing case, where a
// (HttpRequest.BodyPublishers.ofString(...)) argument is appended by the caller.
func javaVerb(method string) string {
	if method == http.MethodPut {
		return "PUT"
	}
	return "POST"
}

// javaBodylessCall is the complete HttpRequest.Builder call for a request with no
// body. GET/DELETE use the no-arg forms; POST/PUT still REQUIRE a body publisher
// in java.net.http (there is no `.POST` field / no-arg overload), so they pass
// BodyPublishers.noBody() — a bare `.POST` would not compile.
func javaBodylessCall(method string) string {
	switch method {
	case http.MethodGet:
		return "GET()"
	case http.MethodDelete:
		return "DELETE()"
	case http.MethodPut:
		return "PUT(HttpRequest.BodyPublishers.noBody())"
	default:
		return "POST(HttpRequest.BodyPublishers.noBody())"
	}
}

// openAPISpec — GET /openapi.json. The full API description (cached).
func (s *Server) openAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(buildSpec())
}

// docsPage — GET /docs. A self-contained Redoc reference UI pointed at
// /openapi.json, loading the offline-embedded Redoc bundle. No external requests
// (fonts default to the system stack).
func (s *Server) docsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(docsHTML))
}

const docsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <title>cronova API reference</title>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><text y='14' font-size='14'>🛰️</text></svg>"/>
  <style>
    /* Redoc's standalone bundle ships a light theme only; pin the page to a light
       surface so it stays legible under a dark-mode OS/browser (otherwise its
       black text renders on the UA's dark canvas). */
    html { color-scheme: light; background: #ffffff; }
    body { margin: 0; padding: 0; background: #ffffff; color: #1a1a2e; font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
    #redoc-loading { padding: 3rem; color: #6b7bff; font: 500 15px/1.5 system-ui, sans-serif; }
  </style>
</head>
<body>
  <div id="redoc-container"><div id="redoc-loading">Loading cronova API reference…</div></div>
  <script src="/redoc.standalone.js"></script>
  <script>
    Redoc.init('/openapi.json', {
      hideDownloadButton: false,
      expandResponses: '200,201',
      requiredPropsFirst: true,
      pathInMiddlePanel: true,
      theme: {
        colors: { primary: { main: '#6b7bff' } },
        typography: {
          fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
          headings: { fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif' },
          code: { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' }
        }
      }
    }, document.getElementById('redoc-container'));
  </script>
</body>
</html>`
