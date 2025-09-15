package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// APIValidatorsActivityResponse represents the response structure for validators activity
type APIValidatorsActivityResponse struct {
	Status string                     `json:"status"`
	Data   *APIValidatorsActivityData `json:"data"`
}

// APIValidatorsActivityData contains the validators activity data
type APIValidatorsActivityData struct {
	Groups         []*APIValidatorActivityGroup `json:"groups"`
	GroupCount     uint64                       `json:"group_count"`
	TotalGroups    uint64                       `json:"total_groups"`
	PageIndex      uint64                       `json:"page_index"`
	TotalPages     uint64                       `json:"total_pages"`
	FirstGroup     uint64                       `json:"first_group"`
	LastGroup      uint64                       `json:"last_group"`
	CurrentEpoch   uint64                       `json:"current_epoch"`
	FinalizedEpoch uint64                       `json:"finalized_epoch"`

	// Filter and grouping options
	GroupBy    uint64 `json:"group_by"`
	SearchTerm string `json:"search_term,omitempty"`
	Sorting    string `json:"sorting"`
}

// APIValidatorActivityGroup represents a group of validators with their activity statistics
type APIValidatorActivityGroup struct {
	Group      string `json:"group"`
	Validators uint64 `json:"validators"`
	Activated  uint64 `json:"activated"`
	Online     uint64 `json:"online"`
	Offline    uint64 `json:"offline"`
	Exited     uint64 `json:"exited"`
	Slashed    uint64 `json:"slashed"`
}

// APIValidatorsActivityV1 returns aggregated validator activity stats
// @Summary Get validators activity statistics
// @Description Returns aggregated validator activity statistics with grouping and filtering options
// @Tags validators
// @Accept json
// @Produce json
// @Param limit query int false "Number of groups to return (max 1000, default 50)"
// @Param page query int false "Page number (starts at 1)"
// @Param group query int false "Grouping option: 1=by 100k indexes, 2=by 10k indexes, 3=by validator names (default: 3 if names available, else 1)"
// @Param search query string false "Search term for group names (supports regex)"
// @Param order query string false "Sort order: group, group-d, count, count-d, active, active-d, online, online-d, offline, offline-d, exited, exited-d, slashed, slashed-d (default: group)"
// @Success 200 {object} APIValidatorsActivityResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/validators/activity [get]
func APIValidatorsActivityV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()
	chainState := services.GlobalBeaconService.GetChainState()
	currentEpoch := chainState.CurrentEpoch()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	// Parse limit parameter
	limit := uint64(50)
	limitStr := query.Get("limit")
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid limit parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedLimit > 1000 {
			parsedLimit = 1000
		}
		if parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Parse page parameter
	pageNumber := uint64(1)
	pageStr := query.Get("page")
	if pageStr != "" {
		parsedPage, err := strconv.ParseUint(pageStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid page parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedPage > 0 {
			pageNumber = parsedPage
		}
	}
	pageIdx := pageNumber - 1

	// Parse grouping parameter
	var groupBy uint64
	if query.Has("group") {
		var err error
		groupBy, err = strconv.ParseUint(query.Get("group"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid group parameter"}`, http.StatusBadRequest)
			return
		}
	}
	if groupBy == 0 {
		if services.GlobalBeaconService.GetValidatorNamesCount() > 0 {
			groupBy = 3
		} else {
			groupBy = 1
		}
	}
	if groupBy < 1 || groupBy > 3 {
		http.Error(w, `{"status": "ERROR: group parameter must be 1, 2, or 3"}`, http.StatusBadRequest)
		return
	}

	// Parse filter parameters
	searchTerm := strings.TrimSpace(query.Get("search"))

	// Parse sort order
	sortOrder := query.Get("order")
	if sortOrder == "" {
		sortOrder = "group"
	}

	// Validate sort order
	validSortOrders := map[string]bool{
		"group": true, "group-d": true,
		"count": true, "count-d": true,
		"active": true, "active-d": true,
		"online": true, "online-d": true,
		"offline": true, "offline-d": true,
		"exited": true, "exited-d": true,
		"slashed": true, "slashed-d": true,
	}
	if !validSortOrders[sortOrder] {
		http.Error(w, `{"status": "ERROR: invalid order parameter"}`, http.StatusBadRequest)
		return
	}

	// Check rate limit
	if err := services.GlobalCallRateLimiter.CheckCallLimit(r, 2); err != nil {
		http.Error(w, `{"status": "ERROR: rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// Get validator activity data using cached function
	pageData, err := getValidatorsActivityAPIData(pageIdx, limit, sortOrder, groupBy, searchTerm)
	if err != nil {
		logrus.WithError(err).Error("failed to get validators activity data")
		http.Error(w, `{"status": "ERROR: failed to get validators activity data"}`, http.StatusInternalServerError)
		return
	}

	// Convert internal model to API model
	apiGroups := make([]*APIValidatorActivityGroup, len(pageData.Groups))
	for i, group := range pageData.Groups {
		apiGroups[i] = &APIValidatorActivityGroup{
			Group:      group.Group,
			Validators: group.Validators,
			Activated:  group.Activated,
			Online:     group.Online,
			Offline:    group.Offline,
			Exited:     group.Exited,
			Slashed:    group.Slashed,
		}
	}

	response := APIValidatorsActivityResponse{
		Status: "OK",
		Data: &APIValidatorsActivityData{
			Groups:         apiGroups,
			GroupCount:     pageData.GroupCount,
			TotalGroups:    pageData.TotalPages * limit, // Approximate total groups
			PageIndex:      pageNumber,
			TotalPages:     pageData.TotalPages,
			FirstGroup:     pageData.FirstGroup,
			LastGroup:      pageData.LastGroup,
			CurrentEpoch:   uint64(currentEpoch),
			FinalizedEpoch: uint64(finalizedEpoch),
			GroupBy:        groupBy,
			SearchTerm:     searchTerm,
			Sorting:        sortOrder,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode validators activity response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

func getValidatorsActivityAPIData(pageIdx uint64, pageSize uint64, sortOrder string, groupBy uint64, searchTerm string) (*models.ValidatorsActivityPageData, error) {
	pageData := &models.ValidatorsActivityPageData{}
	pageCacheKey := fmt.Sprintf("validators_activity_api:%v:%v:%v:%v:%v", pageIdx, pageSize, sortOrder, groupBy, searchTerm)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(processingPage *services.FrontendCacheProcessingPage) interface{} {
		processingPage.CacheTimeout = 10 * time.Second
		return buildValidatorsActivityAPIData(pageIdx, pageSize, sortOrder, groupBy, searchTerm)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ValidatorsActivityPageData)
		if !resOk {
			return nil, fmt.Errorf("invalid page model")
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildValidatorsActivityAPIData(pageIdx uint64, pageSize uint64, sortOrder string, groupBy uint64, searchTerm string) *models.ValidatorsActivityPageData {
	pageData := &models.ValidatorsActivityPageData{
		ViewOptionGroupBy: groupBy,
		Sorting:           sortOrder,
		SearchTerm:        searchTerm,
	}

	if pageSize > 1000 {
		pageSize = 1000
	}
	pageData.PageSize = pageSize
	pageData.CurrentPageIndex = pageIdx + 1

	// Group validators
	validatorGroupMap := map[string]*models.ValidatorsActiviyPageDataGroup{}
	currentEpoch := services.GlobalBeaconService.GetChainState().CurrentEpoch()

	services.GlobalBeaconService.StreamActiveValidatorData(false, func(index phase0.ValidatorIndex, validatorFlags uint16, activeData *beacon.ValidatorData, validator *phase0.Validator) error {
		var groupKey string
		var groupName string

		switch groupBy {
		case 1:
			groupIdx := index / 100000
			groupKey = fmt.Sprintf("%06d", groupIdx)
			groupName = fmt.Sprintf("%v - %v", groupIdx*100000, (groupIdx+1)*100000)
		case 2:
			groupIdx := index / 10000
			groupKey = fmt.Sprintf("%06d", groupIdx)
			groupName = fmt.Sprintf("%v - %v", groupIdx*10000, (groupIdx+1)*10000)
		case 3:
			groupName = services.GlobalBeaconService.GetValidatorName(uint64(index))
			groupKey = strings.ToLower(groupName)
		}

		validatorGroup := validatorGroupMap[groupKey]
		if validatorGroup == nil {
			validatorGroup = &models.ValidatorsActiviyPageDataGroup{
				Group:      groupName,
				GroupLower: groupKey,
				Validators: 0,
				Activated:  0,
				Online:     0,
				Offline:    0,
				Exited:     0,
				Slashed:    0,
			}
			validatorGroupMap[groupKey] = validatorGroup
		}

		validatorGroup.Validators++

		if validatorFlags&beacon.ValidatorStatusSlashed != 0 {
			validatorGroup.Slashed++
		}

		isExited := false
		if activeData != nil && activeData.ActivationEpoch <= currentEpoch {
			if activeData.ExitEpoch > currentEpoch {
				votingActivity := services.GlobalBeaconService.GetValidatorLiveness(index, 3)

				validatorGroup.Activated++
				if votingActivity > 0 {
					validatorGroup.Online++
				} else {
					validatorGroup.Offline++
				}
			} else {
				isExited = true
			}
		} else if validatorFlags&beacon.ValidatorStatusExited != 0 {
			isExited = true
		}

		if isExited {
			validatorGroup.Exited++
		}

		return nil
	})

	// Filter groups based on search term
	validatorGroups := []*models.ValidatorsActiviyPageDataGroup{}

	// Check if search term is a valid regex pattern
	var searchRegex *regexp.Regexp
	if searchTerm != "" {
		// Try to compile as regex
		var err error
		searchRegex, err = regexp.Compile("(?i)" + searchTerm) // Case-insensitive regex
		if err != nil {
			// If not valid regex, fall back to literal string matching
			searchRegex = nil
		}
	}

	for _, group := range validatorGroupMap {
		// Apply search filter
		if searchTerm != "" {
			matched := false

			if searchRegex != nil {
				// Use regex matching
				matched = searchRegex.MatchString(group.Group)
			} else {
				// Fall back to substring matching for invalid regex
				groupNameLower := strings.ToLower(group.Group)
				searchTermLower := strings.ToLower(searchTerm)
				matched = strings.Contains(groupNameLower, searchTermLower)
			}

			if !matched {
				continue
			}
		}

		validatorGroups = append(validatorGroups, group)
	}

	// Sort filtered groups
	switch sortOrder {
	case "group":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return strings.Compare(validatorGroups[a].GroupLower, validatorGroups[b].GroupLower) < 0
		})
	case "group-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return strings.Compare(validatorGroups[a].GroupLower, validatorGroups[b].GroupLower) > 0
		})
	case "count":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Validators < validatorGroups[b].Validators
		})
	case "count-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Validators > validatorGroups[b].Validators
		})
	case "active":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Activated < validatorGroups[b].Activated
		})
	case "active-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Activated > validatorGroups[b].Activated
		})
	case "online":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Online < validatorGroups[b].Online
		})
	case "online-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Online > validatorGroups[b].Online
		})
	case "offline":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Offline < validatorGroups[b].Offline
		})
	case "offline-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Offline > validatorGroups[b].Offline
		})
	case "exited":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Exited < validatorGroups[b].Exited
		})
	case "exited-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Exited > validatorGroups[b].Exited
		})
	case "slashed":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Slashed < validatorGroups[b].Slashed
		})
	case "slashed-d":
		sort.Slice(validatorGroups, func(a, b int) bool {
			return validatorGroups[a].Slashed > validatorGroups[b].Slashed
		})
	}

	groupCount := uint64(len(validatorGroups))

	startIdx := pageIdx * pageSize
	endIdx := startIdx + pageSize
	if startIdx >= groupCount {
		validatorGroups = []*models.ValidatorsActiviyPageDataGroup{}
	} else if endIdx > groupCount {
		validatorGroups = validatorGroups[startIdx:]
	} else {
		validatorGroups = validatorGroups[startIdx:endIdx]
	}
	pageData.Groups = validatorGroups
	pageData.GroupCount = uint64(len(validatorGroups))

	pageData.TotalPages = groupCount / pageSize
	if groupCount%pageSize != 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages - 1
	pageData.FirstGroup = startIdx
	pageData.LastGroup = endIdx

	if pageIdx >= 1 {
		pageData.PrevPageIndex = pageIdx
	}
	if endIdx <= groupCount {
		pageData.NextPageIndex = pageIdx + 1
	}

	return pageData
}