package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/table"
)

// TableSettingsView is GET/PUT /api/settings/table: the table settings (spec 02 §13, D17) and
// the current preset (onboarding step 6 "game defaults").
type TableSettingsView struct {
	table.Settings
	CurrentPresetID int64 `json:"current_preset_id"`
}

func (o Options) settingsRoutes(api *gin.RouterGroup) {
	t := o.Table
	g := api.Group("/settings", RequireAdmin)
	g.GET("/table", func(c *gin.Context) {
		p, err := t.CurrentPreset(c.Request.Context())
		if err != nil {
			engineError(c, err)
			return
		}
		c.JSON(http.StatusOK, TableSettingsView{Settings: t.Settings(), CurrentPresetID: p.ID})
	})
	g.PUT("/table", func(c *gin.Context) {
		in := TableSettingsView{Settings: t.Settings()} // fields left out keep their value
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		if err := t.SetSettings(c.Request.Context(), in.Settings, in.CurrentPresetID); err != nil {
			var se *table.SettingsError
			if errors.As(err, &se) {
				c.AbortWithStatusJSON(http.StatusBadRequest, SettingsErrorView{APIError: APIError{Error: err.Error(), Code: "invalid_setting"}, Field: se.Field})
				return
			}
			engineError(c, err)
			return
		}
		saved := t.Settings()
		o.audit(c, "table_settings_changed", "", saved.String())
		p, err := t.CurrentPreset(c.Request.Context())
		if err != nil {
			engineError(c, err)
			return
		}
		c.JSON(http.StatusOK, TableSettingsView{Settings: saved, CurrentPresetID: p.ID})
	})
}

// SettingsErrorView is a 400 for one invalid setting.
type SettingsErrorView struct {
	APIError
	Field string `json:"field"`
}
