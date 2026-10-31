package isureal

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type createFriendRequestRequest struct {
	RecipientID string `json:"recipientId"`
}

type createFriendRequestResponse struct {
	RelationID string    `json:"relationId"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func createFriendRequestHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input createFriendRequestRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		actor := GetSession(c).UserID
		if err := validateRelationshipTarget(actor, input.RecipientID); err != nil {
			return err
		}
		created, err := createFriendRequest(c.Request().Context(), h.DB, actor, input.RecipientID)
		if err != nil {
			return err
		}
		switch created.Result {
		case "not_found":
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		case "conflict":
			return NewAPIError(http.StatusConflict, "relationship_conflict")
		}
		return c.JSON(http.StatusCreated, createFriendRequestResponse{RelationID: *created.RelationID, Status: *created.Status, UpdatedAt: *created.UpdatedAt})
	}
}

type transitionFriendRequestRequest struct {
	RequestID string `param:"requestId" json:"-"`
	Action    string `param:"action" json:"-"`
}

type transitionFriendRequestResponse createFriendRequestResponse

func transitionFriendRequestHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input transitionFriendRequestRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		var status string
		switch input.Action {
		case "accept":
			status = "accepted"
		case "reject":
			status = "rejected"
		case "cancel":
			status = "cancelled"
		default:
			return NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
		}
		transitioned, err := transitionFriendRequest(c.Request().Context(), h.DB, GetSession(c).UserID, input.RequestID, status)
		if err != nil {
			return err
		}
		switch transitioned.Result {
		case "not_found":
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		case "forbidden":
			return NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
		case "conflict":
			return NewAPIError(http.StatusConflict, "relationship_conflict")
		case "invalid":
			return NewAPIError(http.StatusBadRequest, "invalid_relationship")
		}
		return c.JSON(http.StatusOK, transitionFriendRequestResponse{RelationID: *transitioned.RelationID, Status: *transitioned.Status, UpdatedAt: *transitioned.UpdatedAt})
	}
}
