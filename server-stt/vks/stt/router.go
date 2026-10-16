package stt

import (
	"net/http"

	"github.com/Piloswine1/fizz"
	"github.com/Piloswine1/gadgeto/tonic"
)

func AddRoutes(group *fizz.RouterGroup) {
	sttGroup := group.Group("/stt", "stt related", "(AGENT API)")

	controller := NewSttAgentHandlers()

	sttGroup.POST("/:room", []fizz.OperationOption{
		fizz.Summary("Start test session"),
	}, tonic.Handler(controller.StartStt, http.StatusOK))

	sttGroup.DELETE("/:room", []fizz.OperationOption{
		fizz.Summary("Start test session"),
	}, tonic.Handler(controller.StopStt, http.StatusOK))
}
