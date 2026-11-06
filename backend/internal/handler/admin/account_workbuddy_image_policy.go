package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) UpdateWorkBuddyImagePolicy(c *gin.Context) {
	if adminaccess.FromContext(c.Request.Context()) != nil {
		response.Error(c, http.StatusForbidden, "Image policy requires full administrator access")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var input struct {
		NativeRetryAccepted *bool `json:"native_retry_accepted"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.NativeRetryAccepted == nil {
		response.BadRequest(c, "native_retry_accepted must be a boolean")
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Expected one image policy object")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	if !service.WorkBuddyImagePolicyEligible(account) {
		response.BadRequest(c, "An OpenAI API key account with an exact WorkBuddy image model mapping is required")
		return
	}
	accepted := *input.NativeRetryAccepted
	// Key-level repository merging preserves verified mode and all other runtime/account state.
	err = h.adminService.UpdateAccountExtra(c.Request.Context(), id, service.WorkBuddyImageNativeRetryPolicy(accepted))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"native_retry_accepted": accepted})
}
