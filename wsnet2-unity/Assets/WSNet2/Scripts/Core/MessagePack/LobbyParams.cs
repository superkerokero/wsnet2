using System.Collections.Generic;
using MessagePack;

namespace WSNet2.Core
{
    [MessagePackObject]
    public class CreateParam
    {
        [Key("room")]
        public RoomOption roomOption;

        [Key("client")]
        public ClientInfo clientInfo;
    }

    [MessagePackObject]
    public class JoinParam
    {
        [Key("query")]
        public List<List<Query.Condition>> queries;

        [Key("client")]
        public ClientInfo clientInfo;
    }

    [MessagePackObject]
    public class SearchParam
    {
        [Key("group")]
        public uint group;

        [Key("query")]
        public List<List<Query.Condition>> queries;

        [Key("limit")]
        public int limit;

        [Key("joinable")]
        public bool checkJoinable;

        [Key("watchable")]
        public bool checkWatchable;
    }
}
