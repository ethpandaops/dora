package handlers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/types/models"
)

// parseElPageParam parses the page identity used by the keyset EL list pages.
// The last segment is always the page number; the first is the txUid cursor.
// Transactions use "<txUid>-<pageNum>" (txUid already encodes the tx index);
// transfers use "<txUid>-<txIdx>-<pageNum>" with the extra transfer sub-index.
// Missing or malformed input yields the first page: (0, 0, 1).
func parseElPageParam(p string) (uint64, uint32, uint64) {
	txUid, txIdx, pageNum := uint64(0), uint32(0), uint64(1)
	if p == "" {
		return txUid, txIdx, pageNum
	}
	parts := strings.Split(p, "-")
	n := len(parts)
	if v, err := strconv.ParseUint(parts[0], 10, 64); err == nil {
		txUid = v
	}
	if n >= 2 {
		if v, err := strconv.ParseUint(parts[n-1], 10, 64); err == nil && v > 0 {
			pageNum = v
		}
	}
	if n >= 3 {
		if v, err := strconv.ParseUint(parts[1], 10, 32); err == nil {
			txIdx = uint32(v)
		}
	}
	return txUid, txIdx, pageNum
}

// buildElPager constructs the pager links for a keyset EL list page.
//
//   - basePath/suffix form the base URL (e.g. "/transactions" + "c=25&reverted=1").
//   - nextA/nextB is the cursor of the last row on this page (for the > link).
//   - hasPrev/atFirst/prevA/prevB come from the previous-anchor probe: hasPrev is
//     true while any newer row exists; atFirst means the previous page is the
//     newest page; otherwise prevA/prevB is the previous page's boundary cursor.
func buildElPager(basePath, suffix string, pageNum uint64, hasNext bool, nextA uint64, nextB uint32, hasPrev, atFirst bool, prevA uint64, prevB uint32, withSubIdx bool) *models.PagerData {
	// cursor formats the page identity: "<txUid>" for transactions (txUid already
	// encodes the tx index) or "<txUid>-<txIdx>" for transfers.
	cursor := func(a uint64, b uint32) string {
		if withSubIdx {
			return fmt.Sprintf("%d-%d", a, b)
		}
		return fmt.Sprintf("%d", a)
	}
	first := fmt.Sprintf("%s?%s", basePath, suffix)
	pager := &models.PagerData{
		PageNum:   pageNum,
		HasNext:   hasNext,
		HasPrev:   hasPrev,
		FirstLink: first,
	}
	if hasNext {
		pager.NextLink = fmt.Sprintf("%s&p=%s-%d", first, cursor(nextA, nextB), pageNum+1)
	}
	if hasPrev {
		if atFirst {
			pager.PrevLink = first
		} else {
			prevNum := pageNum - 1
			if prevNum < 1 {
				prevNum = 1
			}
			pager.PrevLink = fmt.Sprintf("%s&p=%s-%d", first, cursor(prevA, prevB), prevNum)
		}
	}
	return pager
}

// buildOffsetPager constructs a classic offset pager (First | < | [input] / M |
// > | Last) for pages that know their total page count. params are preserved as
// hidden fields on the page-jump form and on every link; the page number uses
// the "p" query parameter.
func buildOffsetPager(action string, params []models.UrlParam, pageNum, totalPages uint64) *models.PagerData {
	mk := func(p uint64) string {
		q := url.Values{}
		for _, kv := range params {
			q.Set(kv.Key, kv.Value)
		}
		q.Set("p", strconv.FormatUint(p, 10))
		return action + "?" + q.Encode()
	}
	pager := &models.PagerData{
		PageNum:       pageNum,
		TotalPages:    totalPages,
		HasPrev:       pageNum > 1,
		HasNext:       pageNum < totalPages,
		HasLast:       pageNum < totalPages,
		ShowInput:     true,
		JumpAction:    action,
		JumpParams:    params,
		JumpPageField: "p",
	}
	if pager.HasPrev {
		pager.FirstLink = mk(1)
		pager.PrevLink = mk(pageNum - 1)
	}
	if pager.HasNext {
		pager.NextLink = mk(pageNum + 1)
	}
	if pager.HasLast {
		pager.LastLink = mk(totalPages)
	}
	return pager
}
