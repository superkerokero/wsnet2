package cmd

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"wsnet2/binary"
	"wsnet2/client"
	"wsnet2/pb"
)

var (
	soakRoomCount   int
	soakMinLifeTime time.Duration
	soakMaxLifeTime time.Duration
)

// soakCmd runs soak test
//
// 耐久性テスト
//  1. masterが部屋を作成し、player*2, watcher*5 が入室する
//  2. 部屋が作成されてから指定範囲のランダムな時間が経過したらmasterは退室する
//     - masterが退室したらplayerも退室して部屋が終了する
//     - watcherは部屋が終了するまでいつづける
//  3. 1,2を指定並列数で動かし続ける
//     - およそ指定並列数の部屋が常に存在する状態を維持
//
// 送信メッセージ
//  1. master
//     - 1500byteを0.2秒間隔で5秒(25回)、4000byteを1秒間隔で5回 broadcast
//     - 30~60byteをランダムに毎秒 broadcast
//     - 5秒に1回PublicPropを書きかえ
//  2. player
//     - 1500byteを0.2秒間隔で5秒(25回)、4000byteを1秒間隔で5回 ToMaster
//     - 30~60byteをランダムに毎秒 ToMaster
//  3. watcher
//     - 30~60byteをランダムに10秒毎 ToMaster
var soakCmd = &cobra.Command{
	Use:   "soak",
	Short: "Run soak test",
	Long:  `soak test (耐久性テスト): 指定した範囲の寿命の部屋を、指定数並列に動かし続ける`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSoak(cmd.Context(), soakRoomCount, soakMinLifeTime, soakMaxLifeTime)
	},
}

func init() {
	rootCmd.AddCommand(soakCmd)

	soakCmd.Flags().IntVarP(&soakRoomCount, "room-count", "c", 10, "Parallel room count")
	soakCmd.Flags().DurationVarP(&soakMinLifeTime, "min-life-time", "m", 10*time.Minute, "Minimum life time")
	soakCmd.Flags().DurationVarP(&soakMaxLifeTime, "max-life-time", "M", 20*time.Minute, "Maximum life time")
}

// runSoak runs soak test
func runSoak(ctx context.Context, roomCount int, minLifeTime, maxLifeTime time.Duration) error {
	if roomCount < 1 {
		return fmt.Errorf("room count must be greater than 0")
	}
	if minLifeTime > maxLifeTime {
		return fmt.Errorf("min life time must be less than max life time")
	}
	lifetimeRange := int(maxLifeTime - minLifeTime)

	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	ech := make(chan error)
	counter := make(chan struct{}, roomCount)

	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ech:
			cancel()
			return err
		case counter <- struct{}{}:
		}

		wg.Add(1)
		go func(n int) {
			lifetime := minLifeTime
			if lifetimeRange != 0 {
				lifetime += time.Duration(rand.Intn(lifetimeRange))
			}
			err := runSoakRoom(ctx, n, lifetime)
			if err != nil {
				ech <- err
			}
			wg.Done()
			<-counter
		}(n)

		time.Sleep(time.Second)
	}
}

// runSoakRoom runs a room
func runSoakRoom(ctx context.Context, n int, lifetime time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	logprefix := fmt.Sprintf("room[%d]", n)
	masterId := fmt.Sprintf("master-%d", n)
	props := binary.Dict{
		"room":  binary.MarshalStr8(fmt.Sprintf("soak-%d", n)),
		"score": binary.MarshalInt(0),
	}

	room, master, err := createRoom(ctx, masterId, &pb.RoomOption{
		Visible:     true,
		Joinable:    true,
		Watchable:   true,
		SearchGroup: SoakSearchGroup,
		PublicProps: binary.MarshalDict(props),
	})
	if err != nil {
		return err
	}
	logger.Infof("%s start %v lifetime=%v", logprefix, room.Id, lifetime)

	var rttSum, rttCnt, rttMax int64
	var avg float64
	wg.Add(1)
	go func() {
		rttSum, rttCnt, rttMax, avg = runMaster(ctx, master, lifetime, logprefix)
		wg.Done()
	}()

	time.Sleep(time.Second) // wait for refleshing cache of the lobby

	for i := 0; i < 2; i++ {
		playerId := fmt.Sprintf("player-%v-%v", n, i)

		q := client.NewQuery()
		q.Equal("room", room.PublicProps["room"])

		_, player, err := joinRandom(ctx, playerId, SoakSearchGroup, q)
		if err != nil {
			return err
		}

		wg.Add(1)
		go func() {
			runPlayer(ctx, player, masterId, logprefix)
			wg.Done()
		}()
	}

	for i := 0; i < 5; i++ {
		watcherId := fmt.Sprintf("watcher-%v-%v", n, i)

		_, watcher, err := watchRoom(ctx, watcherId, room.Id, nil)
		if err != nil {
			return err
		}

		wg.Add(1)
		go func() {
			runWatcher(ctx, watcher, logprefix)
			wg.Done()
		}()
	}

	wg.Wait()
	logger.Infof("%s end RTT sum=%v cnt=%v avg=%v max=%v", logprefix, rttSum, rttCnt, avg, rttMax)
	return nil
}

// runMaster runs a master
func runMaster(ctx context.Context, conn *client.Connection, lifetime time.Duration, logprefix string) (rttSum, rttCnt, rttMax int64, rttAvg float64) {
	clictx, cancel := context.WithCancel(ctx)
	go func() {
		var c <-chan time.Time
		if lifetime > 0 {
			c = time.After(lifetime)
		}
		var msg string
		select {
		case <-ctx.Done():
			msg = "context done"
		case <-c:
			msg = "done"
		}
		cancel()
		conn.Leave(msg)
		logger.Debugf("master leave")
	}()

	// goroutine1: 1500byteを0.2秒間隔で5秒(25回)、4000byteを1秒間隔で5回 broadcast
	go func() {
		for {
			for i := 0; i < 25; i++ {
				select {
				case <-clictx.Done():
					return
				default:
				}
				conn.Send(binary.MsgTypeBroadcast, msgBody[:1500])
				time.Sleep(200 * time.Millisecond)
			}
			for i := 0; i < 5; i++ {
				select {
				case <-clictx.Done():
					return
				default:
				}
				conn.Send(binary.MsgTypeBroadcast, msgBody[:4000])
				time.Sleep(time.Second)
			}
		}
	}()
	// goroutine2: 30~60byteをランダムに毎秒 broadcast
	go func() {
		for {
			select {
			case <-clictx.Done():
				return
			default:
			}
			conn.Send(binary.MsgTypeBroadcast, msgBody[:rand.Intn(30)+30])
			time.Sleep(time.Second)
		}
	}()
	// groutine3: 5秒に1回PublicPropを書きかえ
	go func() {
		for {
			select {
			case <-clictx.Done():
				return
			default:
			}
			conn.Send(binary.MsgTypeRoomProp, binary.MarshalRoomPropPayload(
				true, true, true, SoakSearchGroup, 10, 0,
				binary.Dict{"score": binary.MarshalInt(rand.Intn(1024))}, binary.Dict{}))
			time.Sleep(5 * time.Second)
		}
	}()

	go func() {
		for ev := range conn.Events() {
			switch ev.Type() {
			case binary.EvTypePong:
				p, _ := binary.UnmarshalEvPongPayload(ev.Payload())
				rtt := time.Now().UnixMilli() - int64(p.Timestamp)
				if rtt > RttThreshold {
					logger.Warnf("%s master rtt=%d", logprefix, rtt)
				}
				rttSum += rtt
				rttCnt++
				if rttMax < rtt {
					rttMax = rtt
				}

			case binary.EvTypeLeft:
				p, _ := binary.UnmarshalEvLeftPayload(ev.Payload())
				logger.Infof("%s %v left: %v", logprefix, p.ClientId, p.Cause)
			}
		}
	}()

	msg, err := conn.Wait(ctx)
	if err != nil {
		logger.Errorf("%s master error: %v", logprefix, err)
	}

	logger.Debugf("%s master end: %v", logprefix, msg)
	return rttSum, rttCnt, rttMax, float64(rttSum) / float64(rttCnt)
}

func runPlayer(ctx context.Context, conn *client.Connection, masterId, logprefix string) {
	clictx, cancel := context.WithCancel(ctx)
	defer cancel()

	// goroutine1: 1500byteを0.2秒間隔で5秒(25回)、4000byteを1秒間隔で5回 ToMaster
	go func() {
		for {
			for i := 0; i < 25; i++ {
				select {
				case <-clictx.Done():
					return
				default:
				}
				conn.Send(binary.MsgTypeToMaster, msgBody[:1500])
				time.Sleep(200 * time.Millisecond)
			}
			for i := 0; i < 5; i++ {
				select {
				case <-clictx.Done():
					return
				default:
				}
				conn.Send(binary.MsgTypeToMaster, msgBody[:4000])
				time.Sleep(time.Second)
			}
		}
	}()
	// goroutine2: 30~60byteをランダムに毎秒 ToMaster
	go func() {
		for {
			select {
			case <-clictx.Done():
				return
			default:
			}
			conn.Send(binary.MsgTypeToMaster, msgBody[:rand.Intn(30)+30])
			time.Sleep(time.Second)
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				cancel()
				conn.Leave("context done")
			case ev, ok := <-conn.Events():
				if !ok {
					return
				}
				switch ev.Type() {
				case binary.EvTypeLeft:
					p, err := binary.UnmarshalEvLeftPayload(ev.Payload())
					if err != nil {
						logger.Errorf("%s %v error: UnmarshalEvLeftPayload %v", logprefix, conn.UserId(), err)
						cancel()
						conn.Leave("UnmarshalEvLeftPayload error")
						break
					}

					if p.ClientId == masterId {
						cancel()
						conn.Leave("done")
					}
				}
			}
		}
	}()

	msg, err := conn.Wait(ctx)
	if err != nil {
		logger.Errorf("%s %v error: %v", logprefix, conn.UserId(), err)
	}
	logger.Debugf("%s %v end: %v", logprefix, conn.UserId(), msg)
}

func runWatcher(ctx context.Context, conn *client.Connection, logprefix string) {
	clictx, cancel := context.WithCancel(ctx)
	defer cancel()

	// goroutine1: 30~60byteをランダムに10秒毎 ToMaster
	go func() {
		for {
			select {
			case <-clictx.Done():
				return
			default:
			}
			conn.Send(binary.MsgTypeToMaster, msgBody[:rand.Intn(30)+30])
			time.Sleep(10 * time.Second)
		}
	}()

	go func() {
		for range conn.Events() {
		}
	}()

	// 部屋が自然消滅するまで居続ける

	msg, err := conn.Wait(ctx)
	if err != nil {
		logger.Errorf("%s %v error: %v", logprefix, conn.UserId(), err)
	}
	logger.Debugf("%s %v end: %v", logprefix, conn.UserId(), msg)
}
