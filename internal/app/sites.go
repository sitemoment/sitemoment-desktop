// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
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
