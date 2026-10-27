package config

import (
	"errors"
	"strings"
)

type Delivery struct {
	ReceiptEndpoint                                                              string
	FederationDestination                                                        string
	Enabled                                                                      bool
	ArchiveEnabled                                                               bool
	ArchiveEndpoint                                                              string
	DatabaseURL, Schema, Installation, Publication, Destination, Endpoint, Token string
}

// Disabled delivery reads no database URL or credentials and performs no I/O.
func LoadDelivery(get func(string) string) (Delivery, error) {
	var c Delivery
	flag := get("CONTENT_PUBLISHING_DELIVERY_ENABLED")
	if flag == "" || flag == "false" {
		return c, nil
	}
	if flag != "true" {
		return c, errors.New("CONTENT_PUBLISHING_DELIVERY_ENABLED must be true or false")
	}
	c = Delivery{Enabled: true, DatabaseURL: get("CONTENT_DELIVERY_DATABASE_URL"), Schema: get("CONTENT_SCHEMA"), Installation: get("CONTENT_INSTALLATION_ID"), Publication: get("CONTENT_PUBLICATION_ID"), Destination: get("CONTENT_PUBLISHING_DESTINATION"), Endpoint: get("CONTENT_PUBLISHING_ENDPOINT"), Token: get("CONTENT_PUBLISHING_TOKEN")}
	if strings.TrimSpace(c.DatabaseURL) == "" || c.Schema == "" || c.Installation == "" || c.Publication == "" || c.Destination == "" || c.Endpoint == "" || c.Token == "" {
		return c, errors.New("delivery requires explicit worker database URL, scope, endpoint and token; no credential fallback")
	}
	flag = get("CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED")
	if flag != "" && flag != "false" && flag != "true" {
		return c, errors.New("archive delivery flag must be true or false")
	}
	if flag == "true" {
		c.ArchiveEnabled = true
		c.ArchiveEndpoint = get("CONTENT_PUBLISHING_ARCHIVE_ENDPOINT")
		if c.ArchiveEndpoint == "" {
			return c, errors.New("archive delivery requires its explicit fixed endpoint")
		}
	}
	var err error
	c.FederationDestination, err = LoadFederationProjection(get)
	if err != nil {
		return c, err
	}
	if c.FederationDestination != "" {
		c.ReceiptEndpoint = get("CONTENT_RECEIPT_ENDPOINT")
		if c.ReceiptEndpoint == "" {
			return c, errors.New("enabled federation projection requires an explicit receipt resolver endpoint")
		}
	}
	return c, nil
}
