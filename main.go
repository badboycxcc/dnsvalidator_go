package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 检查DNS是否能解析给定域名
func checkDNS(dnsServer, domain string, wg *sync.WaitGroup, results chan<- string, sem chan struct{}) {
	defer wg.Done()

	// 使用 sem 控制并发
	sem <- struct{}{}
	defer func() { <-sem }() // 释放并发槽

	// 设置超时时间
	timeout := 5 * time.Second
	// 尝试查询
	conn, err := net.DialTimeout("udp", dnsServer+":53", timeout)
	if err != nil {
		// 无法连接
		fmt.Printf("无法连接到 DNS 服务器 %s\n", dnsServer)
		return
	}
	conn.Close()

	// 使用 net 包发送查询请求（这里只是简单检查是否能解析域名）
	_, err = net.LookupHost(domain)
	if err != nil {
		// 无法解析
		fmt.Printf("DNS 服务器 %s 无法解析域名 %s\n", dnsServer, domain)
		return
	}

	// 如果 DNS 服务器能解析域名，输出并保存到结果通道
	fmt.Printf("DNS 服务器 %s 可以解析域名 %s\n", dnsServer, domain)
	results <- dnsServer
}

// 从指定的URL下载DNS服务器列表
func downloadDNSList(url string) ([]string, error) {
	// 发起GET请求
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("无法从 %s 下载 DNS 服务器列表: %v", url, err)
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("无法读取响应体: %v", err)
	}

	// 按行拆分
	lines := strings.Split(string(body), "\n")
	var dnsServers []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			dnsServers = append(dnsServers, line)
		}
	}
	return dnsServers, nil
}

func printUsage() {
	fmt.Println("用法: dns_checker -f <DNS服务器列表文件> [-o <输出文件>] [-t <线程数>] [-d <检查域名>] [-g <在线DNS列表URL>]")
	fmt.Println("  -f  指定 DNS 服务器列表文件路径")
	fmt.Println("  -o  指定输出文件路径 (可选，默认输出到标准输出)")
	fmt.Println("  -t  指定线程数，默认值为 10")
	fmt.Println("  -d  指定检查的域名，默认是 google.com")
	fmt.Println("  -g  从指定 URL 获取 DNS 服务器列表，默认是 https://public-dns.info/nameservers.txt")
	fmt.Println("  -h  打印帮助信息")
}

func main() {
	// 定义命令行参数
	dnsFile := flag.String("f", "", "指定 DNS 服务器列表文件路径")
	outputFile := flag.String("o", "", "指定输出文件路径 (可选，默认输出到标准输出)")
	threads := flag.Int("t", 10, "指定线程数，默认值为 10")
	domain := flag.String("d", "google.com", "指定检查的域名，默认是 google.com")
	gurl := flag.String("g", "https://public-dns.info/nameservers.txt", "从指定 URL 获取 DNS 服务器列表，默认是 https://public-dns.info/nameservers.txt")
	helpFlag := flag.Bool("h", false, "打印帮助信息")

	// 解析命令行参数
	flag.Parse()

	// 如果请求帮助或没有传入任何参数，则打印帮助信息
	if *helpFlag || len(os.Args) == 1 {
		flag.PrintDefaults()
		return
	}

	// 验证 DNS 文件路径是否提供
	if *dnsFile == "" && *gurl == "" {
		fmt.Println("错误: 必须提供 DNS 服务器列表，使用 -f 或 -g 参数.")
		printUsage()
		return
	}

	// 获取 DNS 服务器列表
	var dnsServers []string
	if *gurl != "" {
		// 从URL下载DNS列表
		var err error
		dnsServers, err = downloadDNSList(*gurl)
		if err != nil {
			log.Fatal(err)
		}
	} else if *dnsFile != "" {
		// 从文件读取DNS列表
		file, err := os.Open(*dnsFile)
		if err != nil {
			log.Fatal("无法打开文件：", err)
		}
		defer file.Close()

		// 读取文件中的 DNS 服务器列表
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			dnsServers = append(dnsServers, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Fatal("读取文件时出错：", err)
		}
	}

	// 如果没有提供输出文件路径，则使用标准输出
	var outFile *os.File
	var err error
	if *outputFile != "" {
		// 尝试创建或打开输出文件
		outFile, err = os.Create(*outputFile)
		if err != nil {
			log.Fatal("无法创建输出文件：", err)
		}
		defer outFile.Close()
	} else {
		// 如果没有提供输出文件路径，则输出到标准输出
		outFile = os.Stdout
	}

	// 使用 goroutine 管理并发
	var wg sync.WaitGroup
	results := make(chan string)

	// 创建一个带缓冲区的 channel 来存储结果
	sem := make(chan struct{}, *threads)

	// 读取 DNS 服务器列表并进行并发检查
	for _, dnsServer := range dnsServers {
		dnsServer = strings.TrimSpace(dnsServer)
		if dnsServer == "" {
			continue
		}

		wg.Add(1)

		// 通过 sem 控制并发数
		go func(dnsServer string) {
			checkDNS(dnsServer, *domain, &wg, results, sem)
		}(dnsServer)
	}

	// 等待所有 goroutine 执行完成并关闭 results 通道
	go func() {
		wg.Wait()
		close(results)
	}()

	// 将可用的 DNS 服务器 IP 写入输出文件
	for dns := range results {
		_, err := outFile.WriteString(dns + "\n")
		if err != nil {
			log.Fatal("写入输出文件时出错：", err)
		}
	}

	fmt.Println("所有可用的 DNS 服务器已保存到", *outputFile)
}
