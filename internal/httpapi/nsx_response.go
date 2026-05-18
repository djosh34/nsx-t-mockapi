//nolint:tagliatelle // NSX readonly metadata fields intentionally use leading underscores.
package httpapi

import "net/http"

const searchResponseSchema = "SearchResponse"

type nsxResourceLink struct {
	Href string `json:"href"`
	Rel  string `json:"rel"`
}

type nsxResponseMetadata struct {
	Links  []nsxResourceLink `json:"_links"`
	Schema string            `json:"_schema"`
	Self   nsxResourceLink   `json:"_self"`
}

func newNSXResponseMetadata(req *http.Request, schema string) nsxResponseMetadata {
	self := nsxResourceLink{
		Href: req.URL.RequestURI(),
		Rel:  "self",
	}
	return nsxResponseMetadata{
		Links:  []nsxResourceLink{self},
		Schema: schema,
		Self:   self,
	}
}
