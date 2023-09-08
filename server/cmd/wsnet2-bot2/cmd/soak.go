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
)

const (
	SoakSearchGroup = 10

	RttThreshold = 16 // millisecond
)

var (
	roomCount   int
	minLifeTime time.Duration
	maxLifeTime time.Duration
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
		return runSoak(cmd.Context(), roomCount, minLifeTime, maxLifeTime)
	},
}

func init() {
	rootCmd.AddCommand(soakCmd)

	soakCmd.Flags().IntVarP(&roomCount, "room-count", "c", 10, "Parallel room count")
	soakCmd.Flags().DurationVarP(&minLifeTime, "min-life-time", "m", 10*time.Minute, "Minimum life time")
	soakCmd.Flags().DurationVarP(&maxLifeTime, "max-life-time", "M", 20*time.Minute, "Maximum life time")
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

	var wg sync.WaitGroup
	defer wg.Wait()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
			err := runRoom(ctx, n, lifetime)
			if err != nil {
				ech <- err
			}
			wg.Done()
			<-counter
		}(n)

		time.Sleep(time.Second)
	}
}

// runRoom runs a room
func runRoom(ctx context.Context, n int, lifetime time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	masterId := fmt.Sprintf("master-%d", n)
	props := binary.Dict{
		"room":  binary.MarshalStr8(fmt.Sprintf("soak-%d", n)),
		"score": binary.MarshalInt(0),
	}

	room, master, err := createRoom(ctx, masterId, SoakSearchGroup, props)
	if err != nil {
		return err
	}
	logger.Infof("room[%d] start %v lifetime=%v", n, room.Id, lifetime)

	var rttSum, rttCnt, rttMax int
	var avg float64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		rttSum, rttCnt, avg, rttMax = runMaster(ctx, n, master, lifetime)
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
			runPlayer(ctx, player, n, playerId, masterId)
			wg.Done()
		}()
	}

	for i := 0; i < 5; i++ {
		watcherId := fmt.Sprintf("watcher-%v-%v", n, i)

		_, watcher, err := watchRoom(ctx, watcherId, room.Id)
		if err != nil {
			return err
		}

		wg.Add(1)
		go func() {
			runWatcher(ctx, watcher, n, watcherId)
			wg.Done()
		}()
	}

	wg.Wait()
	logger.Infof("room[%d] end RTT sum=%v cnt=%v avg=%v max=%v", n, rttSum, rttCnt, avg, rttMax)
	return nil
}

// runMaster runs a master
func runMaster(ctx context.Context, n int, conn *client.Connection, lifetime time.Duration) (int, int, float64, int) {
	clictx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		var msg string
		select {
		case <-clictx.Done():
			msg = "context done"
		case <-time.After(lifetime):
			msg = "done"
		}
		cancel()
		conn.Leave(msg)
	}()

	sender := func() {
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
	}

	rttSum := int64(0)
	rttMax := int64(0)
	rttCnt := 0

	go func() {
		for ev := range conn.Events() {
			switch ev.Type() {
			case binary.EvTypePeerReady:
				sender()

			case binary.EvTypePong:
				p, _ := binary.UnmarshalEvPongPayload(ev.Payload())
				rtt := time.Now().UnixMilli() - int64(p.Timestamp)
				if rtt > RttThreshold {
					logger.Warnf("room[%d] master rtt=%d", n, rtt)
				}
				rttSum += rtt
				rttCnt++
				if rttMax < rtt {
					rttMax = rtt
				}

			case binary.EvTypeLeft:
				p, _ := binary.UnmarshalEvLeftPayload(ev.Payload())
				logger.Infof("room[%d] %v left: %v", n, p.ClientId, p.Cause)
			}
		}
	}()

	msg, err := conn.Wait(ctx)
	if err != nil {
		logger.Errorf("room[%v] master error: %v", n, err)
	}

	logger.Debugf("room[%d] master end: %v", n, msg)
	return int(rttSum), rttCnt, float64(rttSum) / float64(rttCnt), int(rttMax)
}

func runPlayer(ctx context.Context, conn *client.Connection, n int, myId, masterId string) {
	clictx, cancel := context.WithCancel(ctx)
	defer cancel()

	sender := func() {
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
	}

	go func() {
		for ev := range conn.Events() {
			switch ev.Type() {
			case binary.EvTypePeerReady:
				sender()

			case binary.EvTypeLeft:
				p, err := binary.UnmarshalEvLeftPayload(ev.Payload())
				if err != nil {
					logger.Errorf("room[%v] %v error: UnmarshalEvLeftPayload %v", n, myId, err)
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
	}()

	msg, err := conn.Wait(ctx)
	if err != nil {
		logger.Errorf("room[%v] %v error: %v", n, myId, err)
	}
	logger.Debugf("room[%v] %v end: %v", n, myId, msg)
}

func runWatcher(ctx context.Context, conn *client.Connection, n int, myId string) {
	clictx, cancel := context.WithCancel(ctx)
	defer cancel()

	sender := func() {
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
	}

	go func() {
		for ev := range conn.Events() {
			switch ev.Type() {
			case binary.EvTypePeerReady:
				sender()
			}
		}
	}()

	// 部屋が自然消滅するまで居続ける

	msg, err := conn.Wait(ctx)
	if err != nil {
		logger.Errorf("room[%v] %v error: %v", n, myId, err)
	}
	logger.Debugf("room[%v] %v end: %v", n, myId, msg)
}
