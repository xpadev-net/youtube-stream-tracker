package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RespondOK sends a 200 OK response with the given data.
func RespondOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, data)
}

// RespondCreated sends a 201 Created response with the given data.
func RespondCreated(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, data)
}

// RespondNoContent sends a 204 No Content response.
func RespondNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// RespondAccepted sends a 202 Accepted response with the given data.
func RespondAccepted(c *gin.Context, data interface{}) {
	c.JSON(http.StatusAccepted, data)
}
