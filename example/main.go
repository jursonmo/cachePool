package main

import (
	"flag"
	"fmt"

	"github.com/jursonmo/cachePool"
)

func main() {
	flag.Parse()
	cp, err := cachePool.NewCachePool(2, 4)
	if err != nil {
		fmt.Println(err)
		return
	}
	key := cachePool.Key{1, 2, 3}
	v := cp.GetValue()
	if v == nil {
		panic("v is nil")
	}
	e := cachePool.GetEntryFromElem(v)
	fmt.Println(e)
	//dosomthing with v
	v.A = 3
	v.B = 2
	v.C = 1

	cp.Store(key, v)
	newv := cp.Load(key)
	if v != newv {
		fmt.Println(v, newv)
		panic("v != newv ")
	}

	fmt.Println("=========showing================")
	fmt.Println(cp.GetPoolPositioner(0))
	ok := cp.DeleteAndFreeValue(key)
	if !ok {
		panic("DeleteAndFreeValue fail")
	}
	ok = cp.DeleteAndFreeValue(key)
	if ok {
		panic("DeleteAndFreeValue double delete fail")
	}

	fmt.Println(cp.GetPoolPositioner(0))

	//测试缓存池自动扩展
	testExtend()
}

func testExtend() {
	fmt.Println("------ testExtend----------------")
	poolNum := 2
	poolCap := 2
	cp, err := cachePool.NewCachePool(poolNum, poolCap)
	if err != nil {
		return
	}
	for i := 0; i < poolCap*poolNum; i++ {
		v := cp.GetValue()
		if v == nil {
			panic("v==nil")
		}
	}

	v := cp.GetValue() //make cp extend pool
	if v == nil {
		panic("v==nil")
	}
	n := cp.GetPoolNum()
	if n != poolNum+1 {
		panic("")
	}
	fmt.Println(cp)
}
