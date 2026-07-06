package store

import (
	"context"
	"testing"
)

// migration060CNYAfter holds the expected price_cny_fen value for every
// built-in slug after migration 060 has run. Since tests run all migrations
// to head before executing, the map also includes the four new tiers
// introduced by migration 061 (max_140x/160x/180x/220x) at their as-inserted
// post-bump values.
var migration060CNYAfter = map[string]int64{
	"free":     0,
	"pro":      13999,
	"mini":     6999,
	"nano":     3499,
	"max_2x":   27999,
	"max_5x":   69999,
	"max_20x":  139999,
	"max_40x":  279999,
	"max_60x":  419999,
	"max_80x":  559999,
	"max_100x": 699999,
	"max_120x": 839999,
	"max_140x": 979999,  // introduced by 061 at post-bump price
	"max_160x": 1119999, // introduced by 061 at post-bump price
	"max_180x": 1259999, // introduced by 061 at post-bump price
	"max_200x": 1399999,
	"max_220x": 1539999, // introduced by 061 at post-bump price
	"max_240x": 1679999,
}

// migration060USDAfter holds the price_usd_cents value that must be
// preserved verbatim across migration 060. Values come from migration 049's
// backfill for pre-049 slugs, from 059 for mini/nano, and from 061 for the
// four new max tiers. Locking these in ensures 060 did not accidentally
// touch the USD column.
var migration060USDAfter = map[string]int64{
	"free":     0,
	"pro":      2000,
	"mini":     1000,
	"nano":     500,
	"max_2x":   4000,
	"max_5x":   10000,
	"max_20x":  20000,
	"max_40x":  40000,
	"max_60x":  60000,
	"max_80x":  80000,
	"max_100x": 100000,
	"max_120x": 120000,
	"max_140x": 140000, // introduced by 061
	"max_160x": 160000, // introduced by 061
	"max_180x": 180000, // introduced by 061
	"max_200x": 200000,
	"max_220x": 220000, // introduced by 061
	"max_240x": 240000,
}

// TestMigration060_CNYBumpedForKnownSlugs asserts every built-in slug's
// price_cny_fen matches the post-060 terminal value.
func TestMigration060_CNYBumpedForKnownSlugs(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration060CNYAfter {
		var got int64
		err := st.pool.QueryRow(ctx,
			`SELECT price_cny_fen FROM plans WHERE slug = $1`, slug).Scan(&got)
		if err != nil {
			t.Fatalf("query slug %s: %v", slug, err)
		}
		if got != want {
			t.Errorf("slug %s: price_cny_fen = %d, want %d", slug, got, want)
		}
	}
}

// TestMigration060_USDUnchanged locks in that migration 060 did not touch
// price_usd_cents on any known slug.
func TestMigration060_USDUnchanged(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration060USDAfter {
		var got int64
		err := st.pool.QueryRow(ctx,
			`SELECT price_usd_cents FROM plans WHERE slug = $1`, slug).Scan(&got)
		if err != nil {
			t.Fatalf("query slug %s: %v", slug, err)
		}
		if got != want {
			t.Errorf("slug %s: price_usd_cents = %d, want %d (must not change across 060)",
				slug, got, want)
		}
	}
}

// TestMigration060_FreeUnchanged is an explicit contract that the free tier
// stays at price_cny_fen=0 regardless of the bump. Overlaps with the map
// above but stands alone in case future edits drop free from the map.
func TestMigration060_FreeUnchanged(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var got int64
	err := st.pool.QueryRow(ctx,
		`SELECT price_cny_fen FROM plans WHERE slug = 'free'`).Scan(&got)
	if err != nil {
		t.Fatalf("query free: %v", err)
	}
	if got != 0 {
		t.Errorf("free tier: price_cny_fen = %d, want 0 (migration 060 must skip free)", got)
	}
}
