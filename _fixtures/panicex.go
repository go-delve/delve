package main

        func F0() {
                defer func() {
                        recover()
                }()
                F1()
        }

        func F1() {
                F2()
        }

        func F2() {
                F3()
        }

        func F3() {
                F4()
        }

        func F4() {
                panic("blah")
        }

        func main() {
                F0()
        }
