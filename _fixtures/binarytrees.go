/* The Computer Language Benchmarks Game
 * http://benchmarksgame.alioth.debian.org/
 *
 * based on Go program by The Go Authors.
 * based on C program by Kevin Carson
 * flag.Arg hack by Isaac Gouy
 * modified by Jamil Djadala to use goroutines
 * modified by Chai Shushan
 *
 */

package main

import (
	"flag"
	"fmt"
	"runtime"
	"strconv"
	"sync"
)

var minDepth = 4
var n = 20

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU() * 2)

	flag.Parse()
	if flag.NArg() > 0 {
		n, _ = strconv.Atoi(flag.Arg(0))
	}

	maxDepth := n
	if minDepth+2 > n {
		maxDepth = minDepth + 2
	}
	stretchDepth := maxDepth + 1

	check_l := bottomUpTree(0, stretchDepth).ItemCheck()
	fmt.Printf("stretch tree of depth %d\t check: %d\n", stretchDepth, check_l)

	longLivedTree := bottomUpTree(0, maxDepth)

	result_trees := make([]int, maxDepth+1)
	result_check := make([]int, maxDepth+1)

	var wg sync.WaitGroup
	for depth_l := minDepth; depth_l <= maxDepth; depth_l += 2 {
		wg.Add(1)
		go func(depth int) {
			iterations := 1 << uint(maxDepth-depth+minDepth)
			check := 0

			for i := 1; i <= iterations; i++ {
				check += bottomUpTree(i, depth).ItemCheck()
				check += bottomUpTree(-i, depth).ItemCheck()
			}
			result_trees[depth] = iterations * 2
			result_check[depth] = check

			wg.Done()
		}(depth_l)
	}
	wg.Wait()

	for depth := minDepth; depth <= maxDepth; depth += 2 {
		fmt.Printf("%d\t trees of depth %d\t check: %d\n",
			result_trees[depth], depth, result_check[depth],
		)
	}
	fmt.Printf("long lived tree of depth %d\t check: %d\n",
		maxDepth, longLivedTree.ItemCheck(),
	)
}

func bottomUpTree(item, depth int) *Node {
	if depth <= 0 {
		return &Node{item, nil, nil}
	}
	return &Node{item,
		bottomUpTree(2*item-1, depth-1),
		bottomUpTree(2*item, depth-1),
	}
}

type Node struct {
	item        int
	left, right *Node
}

func (self *Node) ItemCheck() int {
	if self.left == nil {
		return self.item
	}
	return self.item + self.left.ItemCheck() - self.right.ItemCheck()
}
