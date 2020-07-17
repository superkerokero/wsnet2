package lobby

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/jmoiron/sqlx"
	"golang.org/x/xerrors"
	"google.golang.org/grpc"

	"wsnet2/config"
	"wsnet2/log"
	"wsnet2/pb"
)

type RoomService struct {
	db   *sqlx.DB
	conf *config.LobbyConf
	apps map[string]*pb.App

	roomCache *RoomCache
	gameCache *GameCache
}

func NewRoomService(db *sqlx.DB, conf *config.LobbyConf) (*RoomService, error) {
	query := "SELECT id, `key` FROM app"
	var apps []pb.App
	err := db.Select(&apps, query)
	if err != nil {
		return nil, xerrors.Errorf("select apps error: %w", err)
	}
	rs := &RoomService{
		db:        db,
		conf:      conf,
		apps:      make(map[string]*pb.App),
		roomCache: NewRoomCache(db, time.Millisecond*10),
		gameCache: NewGameCache(db, time.Second*1, conf.ValidHeartBeat),
	}
	for i, app := range apps {
		rs.apps[app.Id] = &apps[i]
	}
	return rs, nil
}

func (rs *RoomService) GetAppKey(appId string) (string, bool) {
	app, found := rs.apps[appId]
	if !found {
		return "", false
	}
	return app.Key, true
}

func (rs *RoomService) Create(appId string, roomOption *pb.RoomOption, clientInfo *pb.ClientInfo) (*pb.JoinedRoomRes, error) {
	if _, found := rs.apps[appId]; !found {
		return nil, xerrors.Errorf("Unknown appId: %v", appId)
	}

	game, err := rs.gameCache.Rand()
	if err != nil {
		return nil, xerrors.Errorf("Join: failed to get game server: %w", err)
	}

	grpcAddr := fmt.Sprintf("%s:%d", game.Hostname, game.GRPCPort)
	conn, err := grpc.Dial(grpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Errorf("client connection error: %v", err)
		return nil, err
	}
	defer conn.Close()

	client := pb.NewGameClient(conn)

	req := &pb.CreateRoomReq{
		AppId:      appId,
		RoomOption: roomOption,
		MasterInfo: clientInfo,
	}

	res, err := client.Create(context.TODO(), req)
	if err != nil {
		fmt.Printf("create room error: %v", err)
		return nil, err
	}

	log.Infof("Created room: %v", res)

	return res, nil
}

func (rs *RoomService) join(appId, roomId string, clientInfo *pb.ClientInfo, hostId uint32) (*pb.JoinedRoomRes, error) {
	game, err := rs.gameCache.Get(hostId)
	if err != nil {
		return nil, xerrors.Errorf("join: failed to get game server: %w", err)
	}

	grpcAddr := fmt.Sprintf("%s:%d", game.Hostname, game.GRPCPort)
	conn, err := grpc.Dial(grpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Errorf("client connection error: %v", err)
		return nil, err
	}
	defer conn.Close()

	client := pb.NewGameClient(conn)

	req := &pb.JoinRoomReq{
		AppId:      appId,
		RoomId:     roomId,
		ClientInfo: clientInfo,
	}

	res, err := client.Join(context.TODO(), req)
	if err != nil {
		fmt.Printf("join room error: %v", err)
		return nil, err
	}

	log.Infof("Joined room: %v", res)

	return res, nil
}

func (rs *RoomService) JoinById(appId, roomId string, clientInfo *pb.ClientInfo) (*pb.JoinedRoomRes, error) {
	if _, found := rs.apps[appId]; !found {
		return nil, xerrors.Errorf("Unknown appId: %v", appId)
	}

	var room pb.RoomInfo
	err := rs.db.Get(&room, "SELECT * FROM room WHERE app_id = ? AND id = ?", appId, roomId)
	if err != nil {
		return nil, xerrors.Errorf("Join: failed to get room: %w", err)
	}

	return rs.join(appId, roomId, clientInfo, room.HostId)

}

func (rs *RoomService) JoinByNumber(appId string, roomNumber int32, clientInfo *pb.ClientInfo) (*pb.JoinedRoomRes, error) {
	if _, found := rs.apps[appId]; !found {
		return nil, xerrors.Errorf("Unknown appId: %v", appId)
	}
	if roomNumber == 0 {
		return nil, xerrors.Errorf("Invalid room number: %v", roomNumber)
	}

	var room pb.RoomInfo
	err := rs.db.Get(&room, "SELECT * FROM room WHERE app_id = ? AND number = ?", appId, roomNumber)
	if err != nil {
		return nil, xerrors.Errorf("JoinByNumber: Failed to get room: %w", err)
	}

	return rs.join(appId, room.Id, clientInfo, room.HostId)
}

func (rs *RoomService) JoinByQuery(appId string, searchGroup uint32, queries []PropQueries, clientInfo *pb.ClientInfo) (*pb.JoinedRoomRes, error) {
	rooms, err := rs.Search(appId, searchGroup, queries, 1000)
	if err != nil {
		return nil, err
	}

	rand.Shuffle(len(rooms), func(i, j int) { rooms[i], rooms[j] = rooms[j], rooms[i] })

	for _, room := range rooms {
		res, err := rs.join(appId, room.Id, clientInfo, room.HostId)
		if err == nil {
			return res, nil
		}
		log.Debugf("JoinByQuery: failed to join room: %w", err)
	}

	return nil, xerrors.Errorf("JoinByQuery: Failed to join all rooms")
}

func (rs *RoomService) Search(appId string, searchGroup uint32, queries []PropQueries, limit int) ([]pb.RoomInfo, error) {
	rooms, props, err := rs.roomCache.GetRooms(appId, searchGroup)
	if err != nil {
		return nil, xerrors.Errorf("RoomCache error: %w", err)
	}

	filtered := make([]pb.RoomInfo, 0, len(rooms))
	for i, r := range rooms {
		for _, q := range queries {
			if q.match(props[i]) {
				filtered = append(filtered, r)
				break
			}
		}
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}

	return filtered, nil
}
