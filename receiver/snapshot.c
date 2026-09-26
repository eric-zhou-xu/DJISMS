#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/IOCFPlugIn.h>
#include <IOKit/usb/IOUSBLib.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <stdlib.h>
#include <CommonCrypto/CommonDigest.h>
#include <ctype.h>
#include <sys/file.h>
#include <fcntl.h>
static int prop(io_service_t s,CFStringRef k){int n=-1;CFTypeRef v=IORegistryEntryCreateCFProperty(s,k,kCFAllocatorDefault,0);if(v){if(CFGetTypeID(v)==CFNumberGetTypeID())CFNumberGetValue(v,kCFNumberIntType,&n);CFRelease(v);}return n;}
int main(int argc,char**argv){
 int lockfd=open("/tmp/djisms-new-module-usb.lock",O_CREAT|O_RDWR|O_NOFOLLOW,0600); if(lockfd<0 || flock(lockfd,LOCK_EX|LOCK_NB)!=0)return 15;
 if(argc!=2)return 10; unsigned loc=(unsigned)strtoul(argv[1],NULL,0); if(loc==0)return 11;
 io_iterator_t it=0;IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostInterface"),&it);io_service_t s,chosen=0;int count=0;
 while((s=IOIteratorNext(it))){if(prop(s,CFSTR("idVendor"))==0x2ca3&&prop(s,CFSTR("idProduct"))==0x4006&&prop(s,CFSTR("bInterfaceNumber"))==2&&(unsigned)prop(s,CFSTR("locationID"))==loc){chosen=s;count++;}else IOObjectRelease(s);}IOObjectRelease(it);if(count!=1){fprintf(stderr,"Expected one IF2, found %d\n",count);return 1;}
 IOCFPlugInInterface **p=NULL;SInt32 score=0;IOReturn r=IOCreatePlugInInterfaceForService(chosen,kIOUSBInterfaceUserClientTypeID,kIOCFPlugInInterfaceID,&p,&score);IOObjectRelease(chosen);if(r||!p){fprintf(stderr,"plugin %x\n",r);return 2;}
 IOUSBInterfaceInterface300 **u=NULL;HRESULT h=(*p)->QueryInterface(p,CFUUIDGetUUIDBytes(kIOUSBInterfaceInterfaceID300),(LPVOID*)&u);(*p)->Release(p);if(h||!u)return 3;
 r=(*u)->USBInterfaceOpen(u);if(r){fprintf(stderr,"open %x\n",r);(*u)->Release(u);return 4;}
 UInt8 num=0,in=0,out=0;(*u)->GetNumEndpoints(u,&num);
 for(int j=1;j<=num;j++){UInt8 dir,n,type,interval;UInt16 size;(*u)->GetPipeProperties(u,j,&dir,&n,&type,&size,&interval);if(type==kUSBBulk&&dir==kUSBIn&&n==4)in=j;if(type==kUSBBulk&&dir==kUSBOut&&n==3)out=j;}
 if(!in||!out){fprintf(stderr,"Unexpected IF2 endpoints\n");return 5;}
 const char* cmds[]={"AT+QGMR","AT+CGSN","AT+CPIN?","AT+CMGF?","AT+CPMS?","AT+CMGL=4",NULL};
 for(int i=0;cmds[i];i++){
 char cmd[128];snprintf(cmd,sizeof(cmd),"%s\r\n",cmds[i]);printf("QUERY %s\n",cmds[i]);fflush(stdout);
 r=(*u)->WritePipeTO(u,out,cmd,(UInt32)strlen(cmd),1000,2000);if(r){fprintf(stderr,"write %x\n",r);(*u)->USBInterfaceClose(u);(*u)->Release(u);return 16;}
 char all[65536]={0};size_t pos=0;int done=0;
 for(int k=0;k<128&&pos<sizeof(all)-513;k++){char b[512];UInt32 len=sizeof(b);r=(*u)->ReadPipeTO(u,in,b,&len,500,1000);if(r)continue;memcpy(all+pos,b,len);pos+=len;all[pos]=0;if(strstr(all,cmds[i]) && (strstr(strstr(all,cmds[i]),"\r\nOK\r\n")||strstr(strstr(all,cmds[i]),"ERROR"))){done=1;break;}}
 if(i==1){char *q=all;int valid=0;while(*q){if(isdigit(*q)&&(q==all||q[-1]=='\n')){int n=0;while(isdigit(q[n]))n++;if(n==15&&(q[n]=='\r'||q[n]=='\n')){unsigned char d[32];char hex[65];CC_SHA256(q,15,d);for(int z=0;z<32;z++)sprintf(hex+z*2,"%02x",d[z]);if(strcmp(hex,"9f14758adcbf238c465ee9dfa6bec722e5973551375eaf3e39fbb962b22cc617")==0)valid=1;}}q++;}if(!valid){(*u)->USBInterfaceClose(u);(*u)->Release(u);return 14;}}
printf("%s\n",all);fflush(stdout);
 if(done && (strstr(all,"ERROR") || (i==0 && !strstr(all,"\r\nQDC507GLEFM21_01.001.02.001\r\n")) || (i==2 && !strstr(all,"+CPIN: READY")) || (i==3 && !strstr(all,"+CMGF: 0")) || (i==4 && !strstr(all,"+CPMS: \"ME\",")))){(*u)->USBInterfaceClose(u);(*u)->Release(u);return 13;}
 if(!done){fprintf(stderr,"Incomplete response; stopping\n");(*u)->USBInterfaceClose(u);(*u)->Release(u);return 12;}
 }
 (*u)->USBInterfaceClose(u);(*u)->Release(u);return 0;
}
