# cachePool 对象池、key value 缓存、减轻gc压力

#### 工作中经常需要一个 key value 的 缓存，并且需要经常修改value的值，所以value必须是一个指针，如果按正常的做法
就是map[key]*struct{xxx},每次make一个value对象，如果这个map比较大，
1. 造成内存碎片化
2. 造成gc扫描的时间过长，
3. map 读写过多于频繁的话，锁竞争就过大，效率低。
#### 为了避免以上这几个问题，要做到以下:
1. map 的key 和 value都不包含指针，避免gc 扫描， bigcache 就是map[int]int方式避免gc扫描。
	验证[slice 和 map 数据类型不同，gc 扫描时间也不同](https://github.com/jursonmo/articles/blob/master/record/go/performent/slice_map_gc.md)
    如果value包含了指针, 用 uintptr 类型代替，并且保证指针指向的内存在cachepool运行期间不会被gc 回收.
    ```go
    type myvalue struct {
    	x int,
    	y uintprt
    }
    ```
    map[int]myvalue{}, myvlaue 对象在 noscan mspan 上，不会被扫描， y 实际就是真正要修改的对象的地址，但要保证这个对象一直存在，否则y将变成野指针。所以这个对象最好就在一个预先分配的不被gc回收的大块内存里。
2. map的操作需要读写锁的保护，但是频繁读写map，竞争过大，效率过低，可以使用shardMap 方式来降低锁的颗粒度从而减小锁的竞争
3. 预先分配一个大的对象池，每次分配value对象时，不用make，直接从对象池里取，用完再放回去。
   （类似于syncPool 的功能，但是syncPool 会make多个小对象且会被gc 扫描回收，即syncPool里的对象只能存在于两次gc之间）
   所以关键是要实现一个效率高的、可伸缩的对象池；
    - 3.1 slots 快速找到一个可用对象在对象池里的位置
    - 3.2 或者用 环形缓存区ring 来记录可用对象的位置。
    - 3.3 spinLock 自旋锁来代替sync.Mutex(目前实现的spinLock需要时间的验证和考验,并且小心使用)
    - 3.4 如果内存不够，自动扩展，新建一个对象池pool

#### all: no pointer in key or value, or value's pointer will not be gc when cachePool is working
    1. slots or ringslots record the free buffer position
    2. map[key]positionID ,positionID indicate the buffer position, so can get the value

#### cachePool 参考 syncPool、bigcache, 但是这两者也有缺点
    1. syncPool 会make多个小对象(内存碎片化)且会被gc 扫描回收，即syncPool里的对象只能存在于两次gc之间
    （1.13.x 的syncPool里的对象也就多存在一个GC的间隔）
    2. bigcache: put 对象时，是把数据包copy到自己维护的内存池里，
    	cachePool 是从内存池里取对象去用，然后store 加入缓存里，这个过程不需要copy，相对bigcache少了一次copy。
    3. bigcache: put 对象时，如果底层内存不够而扩容时，会把原来的池整体拷贝过来；
                 删除对象时，底层的ringbuffer 出现“空洞”，即不能保证能充分利用底层内存来存储value
    
    4. cachePool 获取对象时，借鉴了syncPool，采用Per-P 的方式减少竞争，即优先从当前P对应的Pool里获取对象。slot 使用SpinLock、atomic 避免锁使用。
    
#### TODO：
    1. 自动收缩内存池，即某个pool 使用率不够高，其实是可以在分配内存时不要从这些pool 分配，等待这个pool使用率为0时，可以删除，让gc 回收。

#### 缺点
    不能作为一个库那样使用， 需要把自己 customKey customValue 分别嵌套在 Key 和 Entry 结构里, 且Key Value 是固定的结构, 这导致cachePool不通用，只能作为某种固定对象的缓存、对象池。 

#### 大致构图 
```
cachePool ------> pools            entry0       entry1         entry2
    |           +-------+      +-------------+-------------+-------------+------+
    |           | pool0 | ---->|header|value |header|value |header|value |......
    |           |-------+      +-------------------------------------------------
    |           | pool1 | ---->|header|value |header|value |header|value |......  
    |           |-------+      +-------------+-------------+-------------+------+
    |           | pool2 | ---->|header|value |header|value |header|value |......
    |           |-------+      +-------------------------------------------------
    |           每个pool都分配一个大的内存池或者对象池，每个池平均划分大小相同的内存块entry
    |           每个entry（对象）包含一个header 和value， value是真正使用的内存，而 header 包含entryPosition，用于记录value 的位置信息。
    |           即可以通过header的entryPosition可以找到value的内存地址
    |           那怎么知道哪些entry已经被使用了，哪些未被使用呢？通过一个环形缓存区来维护、记录哪些entry可用.
    |           每个pool都有一个环形缓存区维护、记录其可用entry 信息。
    |       
    |----------->shardmap： size is powerOfTwo
                +------+
                | map0 | 哈希表0
                |------+
                | map1 | 哈希表1
                |------+
                | map2 | 哈希表2
                |------+
                | map3 | 哈希表3
                +------+
                哈希表: 键是代码中使用的Key，值是pool里value的位置信息entryPosition。map[Key]entryPosition,entryPosition不带有指针
                借鉴bigcache的方式，避免gc扫描。
```
过程：
1. cp.GetValue()：
    - 1.1 从pool对应的环形区里拿到一个可用节点， 节点保存的是可用的内存块entry的位置信息。（为了避免读写环形区的竞争, 优先去per-P 对应的pool里找可用对象)
    - 1.2 根据节点可用找到对应可用的内存块entry，entry.Value 就是代码里可操作的内存。
2. cp.Store(key, v)：把Key和Value 缓存到 shardmap里，但是shardmap实际保存的值是entryPosition.
3. cp.Load(key)：通过key 可以找到对象entry的位置信息entryPosition，根据entryPosition便可得到真正对象内存
4. 删除缓存cp.DeleteAndFreeValue(key)：把key从shardmap缓存中删除，同时把Value对应内存块entry put回到环形区里，这块内存就可复用了。
