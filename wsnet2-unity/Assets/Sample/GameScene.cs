﻿using System;
using System.Linq;
using System.Collections.Generic;
using UnityEngine;
using UnityEngine.UI;
using UnityEngine.InputSystem;
using WSNet2.Core;
using Sample.Logic;

namespace Sample
{
    /// <summary>
    /// ゲームシーンのコントローラ
    /// </summary>
    public class GameScene : MonoBehaviour
    {
        /// <summary>
        /// 画面背景の文字
        /// </summary>
        public Text roomText;

        /// <summary>
        /// ボールのアセット
        /// </summary>
        public BallView ballAsset;

        /// <summary>
        /// バーのアセット
        /// </summary>
        public BarView barAsset;

        /// <summary>
        /// 1fr前の入力
        /// </summary>
        public float prevMoveInput;

        /// <summary>
        /// 移動入力
        /// </summary>
        public InputAction moveInput;

        BarView bar1;
        BarView bar2;
        BallView ball;

        BarView playerBar;
        BarView opponentBar;

        GameSimulator simulator;
        GameState state;
        GameTimer timer;
        List<PlayerEvent> events;

        bool isOnlineMode;
        float nextSyncTime;

        string cpuPlayerId
        {
            get
            {
                return "CPU";
            }
        }

        string myPlayerId
        {
            get
            {
                if (WSNet2Runner.Instance != null && WSNet2Runner.Instance.GameRoom != null)
                {
                    return WSNet2Runner.Instance.GameRoom.Me.Id;
                }
                else
                {
                    return "YOU";
                }
            }
        }

        void RoomLog(string s)
        {
            roomText.text += s + "\n";
        }

        void RPCPlayerEvent(string sender, PlayerEvent msg)
        {
            // only master client handle this.
        }

        void RPCSyncGameState(string sender, GameState msg)
        {
            // TODO: How to check if the sender is valid master client?
            if (msg.MasterId == sender)
            {
                state = msg;
                events = events.Where(ev => state.Tick <= ev.Tick).ToList();
            }
        }

        void Awake()
        {
            bar1 = Instantiate(barAsset);
            bar2 = Instantiate(barAsset);
            ball = Instantiate(ballAsset);

            bar1.gameObject.SetActive(false);
            bar2.gameObject.SetActive(false);
            ball.gameObject.SetActive(false);

            moveInput.Enable();

            simulator = new GameSimulator();
            state = new GameState();
            timer = new GameTimer();
            events = new List<PlayerEvent>();
            simulator.Init(state);
            isOnlineMode = WSNet2Runner.Instance != null && WSNet2Runner.Instance.GameRoom != null;
        }

        // Start is called before the first frame update
        void Start()
        {
            if (isOnlineMode)
            {
                var room = WSNet2Runner.Instance.GameRoom;
                roomText.text = "Room:" + room.Id + "\n";

                room.OnError += (e) =>
                {
                    Debug.LogError(e.ToString());
                    RoomLog($"OnError: {e}");
                };

                room.OnErrorClosed += (e) =>
                 {
                     Debug.LogError(e.ToString());
                     RoomLog($"OnErrorClosed: {e}");
                 };

                room.OnJoined += (me) =>
                {
                    RoomLog($"OnJoined: {me.Id}");
                };

                room.OnClosed += (p) =>
                {
                    RoomLog($"OnJoined: {p}");
                };

                room.OnOtherPlayerJoined += (p) =>
                 {
                     RoomLog("OnOtherPlayerJoined:" + p.Id);
                 };

                room.OnOtherPlayerLeft += (p) =>
                {
                    RoomLog("OnOtherPlayerLeft:" + p.Id);
                };

                room.OnMasterPlayerSwitched += (prev, cur) =>
                {
                    RoomLog("OnMasterPlayerSwitched:" + prev.Id + " -> " + cur.Id);
                };

                room.OnPlayerPropertyChanged += (p, _) =>
                {
                    RoomLog($"OnPlayerPropertyChanged: {p.Id}");
                };

                room.OnRoomPropertyChanged += (visible, joinable, watchable, searchGroup, maxPlayers, clientDeadline, publicProps, privateProps) =>
                {
                    RoomLog($"OnRoomPropertyChanged");
                    foreach (var kv in publicProps)
                    {
                        Debug.LogFormat("(public) {0}:{1}", kv.Key, kv.Value.ToString());
                    }
                    foreach (var kv in privateProps)
                    {
                        Debug.LogFormat("(private) {0}:{1}", kv.Key, kv.Value.ToString());
                    }
                };


                var RPCSyncServerTick = new Action<string, long>((sender, tick) =>
                {
                    if (sender == WSNet2Runner.Instance.GameRoom?.Master.Id)
                    {
                        timer.UpdateServerTick(tick);
                    }
                });

                /// 使用するRPCを登録する
                /// MasterClientと同じ順番で同じRPCを登録する必要がある
                room.RegisterRPC<GameState>(RPCSyncGameState);
                room.RegisterRPC<PlayerEvent>(RPCPlayerEvent);
                room.RegisterRPC(RPCSyncServerTick);
                room.Restart();
            }

            events.Add(new PlayerEvent
            {
                Code = PlayerEventCode.Join,
                PlayerId = myPlayerId,
                Tick = timer.NowTick,
            });

            if (!isOnlineMode)
            {
                // オフラインモードのときは WaitingPlayer から始める
                state.Code = GameStateCode.WaitingPlayer;
                events.Add(new PlayerEvent
                {
                    Code = PlayerEventCode.Join,
                    PlayerId = cpuPlayerId,
                    Tick = timer.NowTick,
                });
            }
        }

        void Update()
        {
            Debug.Log(state.Code);

            if (state.Code == GameStateCode.WaitingGameMaster)
            {
                if (Time.frameCount % 10 == 0)
                {
                    var room = WSNet2Runner.Instance.GameRoom;
                    // 本当はルームマスタがルームを作成するシーケンスを想定しているが, サンプルは簡単のため,
                    // マスタークライアントがJoinしてきたら, ルームマスタを委譲する
                    if (room.Me == room.Master)
                    {
                        foreach (var p in room.Players.Values)
                        {
                            if (p.Id.StartsWith("gamemaster"))
                            {
                                RoomLog("Switch master to" + p.Id);
                                room.ChangeRoomProperty(
                                    null, null, null,
                                    null, null, null,
                                    new Dictionary<string, object> { { "gamemaster", p.Id }, { "masterclient", "joined" } },
                                    new Dictionary<string, object> { });
                                room.SwitchMaster(p);
                                break;
                            }
                        }
                    }
                }
            }
            else if (state.Code == GameStateCode.WaitingPlayer)
            {
                if (Time.frameCount % 10 == 0)
                {
                    events.Add(new PlayerEvent
                    {
                        Code = PlayerEventCode.Join,
                        PlayerId = myPlayerId,
                        Tick = timer.NowTick,
                    });
                }
            }
            else if (state.Code == GameStateCode.ReadyToStart)
            {
                if (Time.frameCount % 10 == 0)
                {
                    bar1.gameObject.SetActive(true);
                    bar2.gameObject.SetActive(true);
                    ball.gameObject.SetActive(true);

                    if (state.Player1 == myPlayerId)
                    {
                        playerBar = bar1;
                        opponentBar = bar2;
                    }
                    if (state.Player2 == myPlayerId)
                    {
                        playerBar = bar2;
                        opponentBar = bar1;
                    }

                    events.Add(new PlayerEvent
                    {
                        Code = PlayerEventCode.Ready,
                        PlayerId = myPlayerId,
                        Tick = timer.NowTick,
                    });

                    if (!isOnlineMode)
                    {
                        events.Add(new PlayerEvent
                        {
                            Code = PlayerEventCode.Ready,
                            PlayerId = cpuPlayerId,
                            Tick = timer.NowTick,
                        });
                    }
                }
            }
            else if (state.Code == GameStateCode.InGame)
            {

                var value = moveInput.ReadValue<float>();
                if (value != prevMoveInput)
                {
                    MoveInputCode move = MoveInputCode.Stop;
                    if (0 < value) move = MoveInputCode.Up;
                    if (value < 0) move = MoveInputCode.Down;

                    events.Add(new PlayerEvent
                    {
                        Code = PlayerEventCode.Move,
                        PlayerId = myPlayerId,
                        MoveInput = move,
                        Tick = timer.NowTick,
                    });
                }
                prevMoveInput = value;
            }
            else if (state.Code == GameStateCode.Goal)
            {
                events.Add(new PlayerEvent
                {
                    Code = PlayerEventCode.Ready,
                    PlayerId = myPlayerId,
                    Tick = timer.NowTick,
                });

                if (!isOnlineMode)
                {
                    events.Add(new PlayerEvent
                    {
                        Code = PlayerEventCode.Ready,
                        PlayerId = cpuPlayerId,
                        Tick = timer.NowTick,
                    });
                }
            }

            // オンラインモードならイベントをRPCで送信
            // オフラインモードならローカルのシミュレータに入力
            if (isOnlineMode)
            {
                foreach (var ev in events)
                {
                    WSNet2Runner.Instance.GameRoom.RPC(RPCPlayerEvent, ev);
                }
                simulator.UpdateGame(timer.NowTick, state, events.Where(ev => state.Tick <= ev.Tick));
            }
            else
            {
                Bar cpuBar = null;
                if (state.Player1 == cpuPlayerId) cpuBar = state.Bar1;
                if (state.Player2 == cpuPlayerId) cpuBar = state.Bar2;

                if (cpuBar != null)
                {
                    MoveInputCode move = MoveInputCode.Stop;
                    if (state.Ball.Position.y < cpuBar.Position.y) move = MoveInputCode.Up;
                    if (state.Ball.Position.y > cpuBar.Position.y) move = MoveInputCode.Down;

                    events.Add(new PlayerEvent
                    {
                        Code = PlayerEventCode.Move,
                        PlayerId = cpuPlayerId,
                        MoveInput = move,
                        Tick = timer.NowTick,
                    });
                }
                simulator.UpdateGame(timer.NowTick, state, events);
            }

            if (state.Code == GameStateCode.InGame)
            {
                bar1.UpdatePosition(state.Bar1);
                bar2.UpdatePosition(state.Bar2);
                ball.UpdatePosition(state.Ball);
            }
            events.Clear();
        }
    }
}
