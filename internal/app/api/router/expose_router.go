package router

import "github.com/gin-gonic/gin"

func GetNewRouterInstance() *gin.Engine {
	return NewRouter()
}
