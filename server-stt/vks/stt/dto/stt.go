package dto

type StartSttReq struct {
	Room string `path:"room"`
}

type StopSttReq struct {
	Room string `path:"room"`
}
