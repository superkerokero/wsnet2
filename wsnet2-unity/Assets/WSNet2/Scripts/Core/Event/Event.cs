using System;

namespace WSNet2.Core
{
    /// <summary>
    ///   Gameサーバから送られてくるイベント
    /// </summary>
    public class Event
    {
        const int regularEvType = 30;

        /// <summary>
        ///   イベント種別
        /// </summary>
        public enum EvType
        {
            PeerReady = 1,
            Pong,

            Joined = regularEvType,
            Leave,
            RomProp,
            ClientProp,
            Message,
        }

        /// <summary>
        ///   受信に使ったArraySegmentの中身（使い終わったらバッファプールに返却する用）
        /// </summary>
        public byte[] BufferArray { get; private set; }

        /// <summary>イベント種別</summary>
        public EvType Type { get; private set; }

        /// <summary>通常メッセージか</summary>
        public bool IsRegular { get{ return (int)Type >= regularEvType; } }

        /// <summary>通し番号</summary>
        public uint SequenceNum { get; private set; }

        protected SerialReader reader;

        /// <summary>
        ///   受信バイト列からEventを構築
        /// </summary>
        public static Event Parse(ArraySegment<byte> buf)
        {
            var reader = Serialization.NewReader(buf);
            var type = (EvType)reader.Get8();

            Event ev;
            switch (type)
            {
                case EvType.PeerReady:
                    ev = new EvPeerReady(reader);
                    break;
                case EvType.Joined:
                    ev = new EvJoined(reader);
                    break;
                case EvType.Message:
                    ev = new EvMessage(reader);
                    break;

                default:
                    throw new Exception($"unknown event type: {type}");
            }

            ev.BufferArray = buf.Array;
            return ev;
        }

        /// <summary>
        ///   コンストラクタ
        /// </summary>
        protected Event(EvType type, SerialReader reader)
        {
            this.Type = type;
            this.reader = reader;

            if (IsRegular)
            {
                SequenceNum = reader.Get32();
            }
        }
    }
}
