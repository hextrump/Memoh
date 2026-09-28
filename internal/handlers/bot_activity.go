package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/felinics/memoh/internal/bots"
)

// ListBotActivity godoc
// @Summary List bot activity
// @Description Latest visible message per accessible bot, most recent first, for the messenger-style bot list
// @Tags bots
// @Success 200 {object} bots.ListBotActivityResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /bots/activity [get].
func (h *UsersHandler) ListBotActivity(c echo.Context) error {
	channelIdentityID, err := h.requireChannelIdentityID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	items, err := h.botService.ListAccessible(ctx, channelIdentityID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	activity, err := h.botService.ListActivity(ctx, items)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, bots.ListBotActivityResponse{Items: activity})
}
