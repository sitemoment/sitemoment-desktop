// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// SiteService manages the local site list.
type SiteService struct {
	Store *store.Store
	Sync  func(siteID int64) // starts a background sync
}

func (s *SiteService) List(ctx context.Context) ([]store.Site, error) { return s.Store.Sites(ctx) }

func (s *SiteService) Add(ctx context.Context, host string) (store.Site, error) {
	return s.Store.AddSite(ctx, host, time.Now().Unix())
}

func (s *SiteService) Remove(ctx context.Context, id int64) error { return s.Store.DeleteSite(ctx, id) }

// BindGSC links the site to a Search Console property and starts syncing it.
func (s *SiteService) BindGSC(ctx context.Context, id int64, account, property string) error {
	if err := s.Store.BindGSC(ctx, id, account, property); err != nil {
		return err
	}
	if s.Sync != nil {
		s.Sync(id)
	}
	return nil
}

// BindGA links the site to a GA4 property (both empty: unbind) and starts
// syncing it.
func (s *SiteService) BindGA(ctx context.Context, id int64, account, property string) error {
	if (account == "") != (property == "") {
		return errors.New("GA4 需要同时选择 Google 账号和媒体资源")
	}
	if property != "" && !strings.HasPrefix(property, "properties/") {
		return errors.New("GA4 媒体资源的格式不对")
	}
	if err := s.Store.BindGA(ctx, id, account, property); err != nil {
		return err
	}
	if s.Sync != nil {
		s.Sync(id)
	}
	return nil
}

// BindBing links the site to a Bing Webmaster site through a saved key
// (both empty: unbind) and starts syncing it.
func (s *SiteService) BindBing(ctx context.Context, id int64, account, siteURL string) error {
	if (account == "") != (siteURL == "") {
		return errors.New("Bing 需要同时选择 API key 和站点")
	}
	if err := s.Store.BindBing(ctx, id, account, siteURL); err != nil {
		return err
	}
	if s.Sync != nil {
		s.Sync(id)
	}
	return nil
}
