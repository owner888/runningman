package main

import (
    "flag"
    "fmt"
    "net/http"
    "golang.org/x/net/websocket"
    "container/list"
    "sync"
    "time"
    "runtime"
    "text/template"
    "net"
    //"github.com/favframework/debug"
)

const (
    TIME_FORMAT  = "01-02 15:04:05"
)

var port *int = flag.Int("p", 23456, "Port to listen.")
var connid int
var conns *list.List
var l sync.Mutex
var runningActiveRoom *ActiveRoom = &ActiveRoom{}
//var roomUser = make([]string, 2)
var roomUser [2]string

// 所有活跃用户
type ActiveRoom struct {
    OnlineUsers map[string]*OnlineUser
    Broadcast   chan Message
    CloseSign   chan bool
}

//type RoomUser struct {
//OnlineUsers map[string]*OnlineUser
//}

// 一个在线用户
type OnlineUser struct {
    //InRoom     *ActiveRoom
    Connection *websocket.Conn
    UserInfo   *User
    Send       chan Message
}

// 用户信息
type User struct {
    Utma      string
    Name      string
    Email     string
    Gravatar  string
    MatchUtma string           // 对手utma
    Playing   bool
}

// 信息
type Message struct {
    UserInfo *User
    Event    string
    Content  string
    Time     string
}

// 客户端获取的信息
type JsonMessage struct {
    Event    string
    Msg      string
}

// 用户状态
type UserStatus struct {
    Users []*User
}

func InitRoom() {
    runningActiveRoom = &ActiveRoom{
        OnlineUsers: make(map[string]*OnlineUser),
        Broadcast:   make(chan Message),    // make(chan Message, 256)
        CloseSign:   make(chan bool),
    }
    go runningActiveRoom.run()
}

// Core function of room
func (this *ActiveRoom) run() {
    for {
        select {
        case b := <-this.Broadcast:
            //fmt.Println("Broadcast")
            // 给所有在线用户的发送缓存放入消息
            for _, online := range this.OnlineUsers {
                online.Send <- b
            }
        case c := <-this.CloseSign:
            if c == true {
                close(this.Broadcast)
                close(this.CloseSign)
                return
            }
        }
    }
}

// 接受客户端的消息
func (this *OnlineUser) PullFromClient() {
    for {
        // 从客户端接受信息并且转为 json对象
        var jsonMessage JsonMessage
        err := websocket.JSON.Receive(this.Connection, &jsonMessage)
        // 当用户关闭或者刷新浏览器的时候，这里就会触发一个错误
        if err != nil {
            return
        }

        if jsonMessage.Event == "Run" {
            //fmt.Printf("User %s Running\n", this.UserInfo.Utma)
            m := Message{
                UserInfo : this.UserInfo,
                Event    : "MatchRun",
                Content  : "running",
                Time     : humanCreatedAt(),
            }
            //fmt.Println(this.UserInfo.Utma)
            //fmt.Println(this.UserInfo.MatchUtma)
            // 如果存在竞争对手，给竞争对手发信息
            //if this.UserInfo.MatchUtma != "" {
            if this.UserInfo.Playing {
                // 让自己运动一步
                m.Event = "SelfRun"
                // 判断channel是否被关闭，既客户端是否断网了
                if runningActiveRoom.OnlineUsers[this.UserInfo.Utma].Send != nil {
                    runningActiveRoom.OnlineUsers[this.UserInfo.Utma].Send <- m
                }
                // 如果对手还没有中途退出让对手运动一步
                if this.UserInfo.MatchUtma != "" {
                    m.Event = "MatchRun"
                    // 判断channel是否存在，否则会出现：invalid memory address or nil pointer dereference
                    if runningActiveRoom.OnlineUsers[this.UserInfo.MatchUtma].Send != nil {
                        runningActiveRoom.OnlineUsers[this.UserInfo.MatchUtma].Send <- m
                    }
                }
            }

        }/* else {
            m := Message{
                UserInfo : this.UserInfo,
                Event    : jsonMessage.Event,
                Content  : jsonMessage.Msg,
                Time     : humanCreatedAt(),
            }

            // 先把消息存入广播channel，开始的时候已经有一个协程在监听这个channel了，一旦有消息，就把广播消息给每个用户放一份 online.Send <- b 
            //this.InRoom.Broadcast <- m
            runningActiveRoom.Broadcast <- m
        }*/
    }
}

// 发送消息到客户端
func (this *OnlineUser) PushToClient() {
    // 堵塞检查发送缓存，一旦有消息就给当前用户发送消息
    for b := range this.Send {
        err := websocket.JSON.Send(this.Connection, b)
        if err != nil {
            break
        }
    }
}

// 如果客户端关闭了连接，删除资源
func (this *OnlineUser) killUserResource() {
    this.Connection.Close()
    //delete(this.InRoom.OnlineUsers, this.UserInfo.Utma)
    delete(runningActiveRoom.OnlineUsers, this.UserInfo.Utma)
    close(this.Send)
    fmt.Printf("User %s Closed\n", this.UserInfo.Utma)
    // 如果当前用户在房间里面，清空
    // 其实不用判断roomUser[1]，因为当 roomUser[1] 有值的时候，roomUser[0]、roomUser[1]都会清空了
    if roomUser[0] == this.UserInfo.Utma {
        fmt.Printf("User %s OutRoom\n", this.UserInfo.Utma)
        roomUser[0] = ""
    }

    //m := Message{
        //UserInfo : this.UserInfo,
        //Event    : "close",
        //Content  : "successful",
        //Time     : humanCreatedAt(),
    //}
    //runningActiveRoom.Broadcast <- m

    // 通知对手下线
    if  this.UserInfo.MatchUtma != ""{
        //m := Message{
            //UserInfo : this.UserInfo,
            //Event    : "OutRoom",
            //Content  : "successful",
            //Time     : humanCreatedAt(),
        //}
        //runningActiveRoom.OnlineUsers[this.UserInfo.MatchUtma].Send <- m

        // 清空对手的对手Utma
        runningActiveRoom.OnlineUsers[this.UserInfo.MatchUtma].UserInfo.MatchUtma = ""
    }

}

// 获得在线用户
func (this *ActiveRoom) GetOnlineUsers() (users []*User) {
    for _, online := range this.OnlineUsers {
        users = append(users, online.UserInfo)
    }
    return
}

// 接收连接
func BuildConnection(ws *websocket.Conn) {
    // 获取用户唯一标识
    utma := ws.Request().URL.Query().Get("utma")
    name := ws.Request().URL.Query().Get("name")
    if utma == "" || name == "" {
        return
    }

    //fmt.Printf("jsonServer %#v\n", ws.Config())
    l.Lock()
    connid++
    fmt.Printf("connection id: %d\n", connid)
    l.Unlock()

    // 当前用户信息
    UserInfo := &User{
        Utma      : utma,
        Name      : name,
        Email     : "",
        Gravatar  : "",
        MatchUtma : "",
    }

    // 当前用户在线信息
    onlineUser := &OnlineUser{
        //InRoom     : runningActiveRoom,
        Connection : ws,
        Send       : make(chan Message, 256),
        UserInfo   : UserInfo,
    }
    // 把当前用户在线信息存到在线用户map，这里要加锁，否则map有问题
    // 队列和map都要加锁操作，否则goroutine协程环境下有问题，channel就不用
    l.Lock()
    runningActiveRoom.OnlineUsers[utma] = onlineUser
    //self := conns.PushBack(ws)
    l.Unlock()

    // 如果第一个用户为空
    if roomUser[0] == "" {
        roomUser[0] = utma
    }else if roomUser[1] == "" && roomUser[0] != utma {
        roomUser[1] = utma
    }
    fmt.Printf("User %s[%s] InRoom\n", name, utma)

    // 如果两个位置都有人了，找到对手了
    if  roomUser[0] != "" && roomUser[1] != ""{
        fmt.Printf("Match %s --- %s\n", roomUser[0], roomUser[1])
        // 给自己和对手存入对方的utma
        runningActiveRoom.OnlineUsers[roomUser[0]].UserInfo.MatchUtma = roomUser[1]
        runningActiveRoom.OnlineUsers[roomUser[1]].UserInfo.MatchUtma = roomUser[0]
        // 修改双方游戏状态
        runningActiveRoom.OnlineUsers[roomUser[0]].UserInfo.Playing = true
        runningActiveRoom.OnlineUsers[roomUser[1]].UserInfo.Playing = true
        // 通知双方修改名字
        m := Message{
            UserInfo: UserInfo,
            Event   : "ChangeName",
            Content : "successful",
            Time    : humanCreatedAt(),
        }
        // 发给当前用户，当前用户一定是roomUser[1]，第二位用户
        m.Content = runningActiveRoom.OnlineUsers[roomUser[1]].UserInfo.Name+","+runningActiveRoom.OnlineUsers[roomUser[0]].UserInfo.Name
        runningActiveRoom.OnlineUsers[roomUser[1]].Send <- m
        // 发给对手用户
        m.Content = runningActiveRoom.OnlineUsers[roomUser[0]].UserInfo.Name+","+runningActiveRoom.OnlineUsers[roomUser[1]].UserInfo.Name
        runningActiveRoom.OnlineUsers[roomUser[0]].Send <- m

        // 通知双方开始
        m = Message{
            UserInfo: UserInfo,
            Event   : "Playing",
            Content : "successful",
            Time    : humanCreatedAt(),
        }
        runningActiveRoom.OnlineUsers[roomUser[0]].Send <- m
        runningActiveRoom.OnlineUsers[roomUser[1]].Send <- m

        // 清空，给其他人进来玩
        roomUser[0] = ""
        roomUser[1] = ""
    }
    //godump.Dump(runningActiveRoom.OnlineUsers)

    // 广播消息，放到广播channel里面，前面已经放置了一个协程堵塞处理
    //m := Message{
        //UserInfo: UserInfo,
        //Event   : "connect",
        //Content : "successful",
        //Time    : humanCreatedAt(),
    //}
    //runningActiveRoom.Broadcast <- m
    //name := fmt.Sprintf("user%d", connid)
    //SendMessage(nil, fmt.Sprintf("welcome %s join\n", name))
    fmt.Printf("User %s Connected\n", UserInfo.Utma)

    go onlineUser.PushToClient()
    onlineUser.PullFromClient()
    onlineUser.killUserResource()
}

func getInternal() string{
    addrs, err := net.InterfaceAddrs()
	if err != nil {
        return ""
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
                return ipnet.IP.String()
			}
		}
	}
    return ""
}

func humanCreatedAt() string {
    return time.Now().Format(TIME_FORMAT)
}

// http 服务器
func MainServer(w http.ResponseWriter, req *http.Request) {
    //io.WriteString(w, `<html>`)
    t, err := template.ParseFiles("index.html")
    if (err != nil) {
        fmt.Println(err)
    }
    t.Execute(w, nil)
}

func main() {
    runtime.GOMAXPROCS(runtime.NumCPU());
    fmt.Println("Welcome game server!")
    serverIp := getInternal()
    connid = 0
    // 生成队列
    //conns = list.New()

    flag.Parse()
    http.Handle("/json", websocket.Handler(BuildConnection))
    http.Handle("/static/", http.StripPrefix("/static", http.FileServer(http.Dir("static"))))
    http.HandleFunc("/", MainServer)
    fmt.Printf("http://%s:%d/\n", serverIp, *port)

    go InitRoom()

    err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
    if err != nil {
        panic("ListenANdServe: " + err.Error())
    }
}
