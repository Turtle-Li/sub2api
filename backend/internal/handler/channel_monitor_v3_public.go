package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
	"time"
)

func (h *ChannelMonitorV3Handler) PublicStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var end *time.Time
	if raw := c.Query("end"); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds <= 0 {
			response.BadRequest(c, "invalid end")
			return
		}
		value := time.Unix(seconds, 0)
		end = &value
	}
	status, err := h.service.Status(c.Request.Context(), service.ChannelMonitorV3Viewer{Anonymous: true}, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *ChannelMonitorV3Handler) PublicIncidents(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	result, err := h.service.Incidents(c.Request.Context(), service.ChannelMonitorV3Viewer{Anonymous: true}, page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
