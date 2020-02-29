# cachePool 对象池、key value

#### 工作中经常需要一个 key value 的 缓存，并且需要经常修改value的值，所以value必须是一个指针，如果按正常的做法
就是map[key]*struct{xxx},每次make一个value对象，如果这个map比较大，
1. 造成内存碎片化
2. 造成gc扫描的时间过长，
3. map 读写过多于频繁的话，锁竞争就过大，效率低。
#### 为了避免以上这几个问题，要做到以下:
1. map 的key 和 value都不包含指针，避免gc 扫描， bigcache 就是map[int]int方式避免gc扫描。
	验证[slice 和 map 数据类型不同，gc 扫描时间也不同](https://github.com/jursonmo/articles/blob/master/record/go/performent/slice_map_gc.md)
    如果value包含了指针, 用 uintptr 类型代替，并且保证指针指向的内存在cachepool运行期间不会被gc 回收
2. map的操作需要读写锁的保护，但是频繁读写map，竞争过大，效率过低，可以使用shardMap 方式减小锁的竞争
3. 预先分配一个大的对象池，每次分配value对象时，不用make，直接从对象池里去，用完再放回去。
   （类似于syncPool 的功能，但是syncPool 会make多个小对象且会被gc 扫描回收，即syncPool里的对象只能存在于两次gc之间）
   所以关键是要实现一个效率高的、可伸缩的对象池；
    - 3.1 slots 快速找到一个可用的对象
    - 3.2 spinLock 自旋锁(需要时间的验证和考验,并且小心使用)
    - 3.3 或者用 环形缓存区ring 来记录对象位置。
    - 3.4 如果内存不够，自动新建一个pool

all: no pointer in key or value, or value's pointer will not be gc when cachePool is working
     1. slots or ringslots record the free buffer position
     2. map[key]positionID ,positionID indicate the buffer position, so can get the value

#### cachePool 参考 syncPool、bigcache, 但是这两者也有缺点
    1. syncPool 会make多个小对象(内存碎片化)且会被gc 扫描回收，即syncPool里的对象只能存在于两次gc之间
    （1.13.x 的syncPool里的对象也就多存在一个GC的间隔）
    2. bigcache: 删除时，底层的ringbuffer 出现“空洞”，即不能保证能充分利用底层内存来存储value

#### TODO：
    1. 自动收缩内存池，即某个pool 使用率不够高，其实是可以在分配内存时不要从这些pool 分配，等待这个pool使用率为0时，可以删除，让gc 回收。

#### 缺点
    不能作为一个库那样使用， 需要把自己 customKey customValue 分别嵌套在 Key 和 Entry 结构里。
