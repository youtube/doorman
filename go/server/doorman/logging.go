// Copyright 2016 Google, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package doorman

import (
	"github.com/golang/glog"
	"google.golang.org/grpc/grpclog"
)

type glogger struct{}

func (g *glogger) Fatal(args ...interface{}) {
	glog.Fatal(args...)
}

func (g *glogger) Fatalf(format string, args ...interface{}) {
	glog.Fatalf(format, args...)
}

func (g *glogger) Fatalln(args ...interface{}) {
	glog.Fatalln(args...)
}

func (g *glogger) Print(args ...interface{}) {
	glog.Info(args...)
}

func (g *glogger) Printf(format string, args ...interface{}) {
	glog.Infof(format, args...)
}

func (g *glogger) Println(args ...interface{}) {
	glog.Infoln(args...)
}

func init() {
	grpclog.SetLogger(&glogger{})
}
