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
#include <errno.h>
#include <signal.h>
static volatile sig_atomic_t stopping=0;
static void stopwatch(int sig){stopping=1;}
static int prop(io_service_t s,CFStringRef k){int n=-1;CFTypeRef v=IORegistryEntryCreateCFProperty(s,k,kCFAllocatorDefault,0);if(v){if(CFGetTypeID(v)==CFNumberGetTypeID())CFNumberGetValue(v,kCFNumberIntType,&n);CFRelease(v);}return n;}

static int query(IOUSBInterfaceInterface300 **u,UInt8 in,UInt8 out,const char *command,char *all,size_t capacity){
 char cmd[160];snprintf(cmd,sizeof(cmd),"%s\r\n",command);
 IOReturn r=(*u)->WritePipeTO(u,out,cmd,(UInt32)strlen(cmd),1000,2000);if(r)return 16;
 size_t pos=0;all[0]=0;
 for(int k=0;k<20&&pos<capacity-513;k++){
  char b[512];UInt32 len=sizeof(b);r=(*u)->ReadPipeTO(u,in,b,&len,500,1000);if(r)continue;
  memcpy(all+pos,b,len);pos+=len;all[pos]=0;
  if(strstr(all,"\r\nERROR\r\n")||strstr(all,"\r\n+CMS ERROR:")||strstr(all,"\r\n+CME ERROR:"))return 13;
  if(strstr(all,"\r\nOK\r\n"))return 0;
 }
 return 12;
}
static int identity(const char *all){
 for(const char *q=all;*q;q++)if(isdigit(*q)&&(q==all||q[-1]=='\n')){
  int n=0;while(isdigit(q[n]))n++;
  if(n==15&&(q[n]=='\r'||q[n]=='\n')){unsigned char d[32];char hex[65];CC_SHA256(q,15,d);for(int z=0;z<32;z++)sprintf(hex+z*2,"%02x",d[z]);if(!strcmp(hex,"9f14758adcbf238c465ee9dfa6bec722e5973551375eaf3e39fbb962b22cc617"))return 1;}
 }return 0;
}
/* Only an exact, whole PDU line in a successful CMGR response can authorize deletion. */
static int same_pdu(const char *reply,const char *expected){
 const char *h=strstr(reply,"+CMGR:");if(!h)return 0;h=strchr(h,'\n');if(!h)return 0;h++;
 size_t n=strcspn(h,"\r\n");return n==strlen(expected)&&!strncmp(h,expected,n);
}
int main(int argc,char**argv){
 int lockfd=open("/tmp/djisms-new-module-usb.lock",O_CREAT|O_RDWR|O_NOFOLLOW,0600); if(lockfd<0 || flock(lockfd,LOCK_EX|LOCK_NB)!=0)return 15;
 int watching=argc==3&&!strcmp(argv[2],"--watch");int deleting=argc==4&&!strcmp(argv[2],"--delete");if(argc!=3&&!deleting)return 10;const char *store=watching?"ME":argv[deleting?3:2];if(strcmp(store,"ME")&&strcmp(store,"SM"))return 10; unsigned loc=(unsigned)strtoul(argv[1],NULL,0); if(loc==0)return 11;
 io_iterator_t it=0;IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostInterface"),&it);io_service_t s,chosen=0;int count=0;
 while((s=IOIteratorNext(it))){if(prop(s,CFSTR("idVendor"))==0x2ca3&&prop(s,CFSTR("idProduct"))==0x4006&&prop(s,CFSTR("bInterfaceNumber"))==2&&(unsigned)prop(s,CFSTR("locationID"))==loc){chosen=s;count++;}else IOObjectRelease(s);}IOObjectRelease(it);if(count!=1){fprintf(stderr,"Expected one IF2, found %d\n",count);return 1;}
 IOCFPlugInInterface **p=NULL;SInt32 score=0;IOReturn r=IOCreatePlugInInterfaceForService(chosen,kIOUSBInterfaceUserClientTypeID,kIOCFPlugInInterfaceID,&p,&score);IOObjectRelease(chosen);if(r||!p){fprintf(stderr,"plugin %x\n",r);return 2;}
 IOUSBInterfaceInterface300 **u=NULL;HRESULT h=(*p)->QueryInterface(p,CFUUIDGetUUIDBytes(kIOUSBInterfaceInterfaceID300),(LPVOID*)&u);(*p)->Release(p);if(h||!u)return 3;
 r=(*u)->USBInterfaceOpen(u);if(r){fprintf(stderr,"open %x\n",r);(*u)->Release(u);return 4;}
 UInt8 num=0,in=0,out=0;(*u)->GetNumEndpoints(u,&num);
 for(int j=1;j<=num;j++){UInt8 dir,n,type,interval;UInt16 size;(*u)->GetPipeProperties(u,j,&dir,&n,&type,&size,&interval);if(type==kUSBBulk&&dir==kUSBIn&&n==4)in=j;if(type==kUSBBulk&&dir==kUSBOut&&n==3)out=j;}
 if(!in||!out){fprintf(stderr,"Unexpected IF2 endpoints\n");return 5;}

 char expected[1024]={0};unsigned slot=0;
 if(deleting){char line[1200],extra;if(!fgets(line,sizeof(line),stdin)||sscanf(line,"%u %1023s %c",&slot,expected,&extra)!=2||slot>65535)return 20;size_t n=strlen(expected);if(n<2||n%2)return 20;for(size_t j=0;j<n;j++)if(!isxdigit(expected[j])||islower(expected[j]))return 20;}
 const char* cmds[]={"AT+QGMR","AT+CGSN","AT+CPIN?","AT+CMGF?","AT+CPMS?",NULL};
 char all[65536];int result=0;char original[8]={0};int changed=0;char oldcnmi[96]={0};
 for(int i=0;cmds[i];i++){
  result=query(u,in,out,cmds[i],all,sizeof(all));if(result)goto end;
  if((i==0&&!strstr(all,"\r\nQDC507GLEFM21_01.001.02.001\r\n"))||(i==1&&!identity(all))||(i==2&&!strstr(all,"+CPIN: READY"))||(i==3&&!strstr(all,"+CMGF: 0"))){result=14;goto end;}
  if(i==4){char *cp=strstr(all,"+CPMS:");if(!cp||sscanf(cp,"+CPMS: \"%7[A-Z]\"",original)!=1||(strcmp(original,"ME")&&strcmp(original,"SM")&&strcmp(original,"MT"))){result=14;goto end;}}
  if(!watching&&!deleting&&i!=1)printf("QUERY %s\n%s\n",cmds[i],all);
 }
 if(!watching&&!deleting&&!strcmp(store,"ME")){
  const char *info[]={"AT+COPS?","AT+CSQ","AT+CEREG?","AT+CNUM","AT+QCCID",NULL};
  for(int j=0;info[j];j++)if(query(u,in,out,info[j],all,sizeof(all))==0)printf("INFO %s\n%s\n",info[j],all);
 }
 if(watching){
  signal(SIGTERM,stopwatch);signal(SIGINT,stopwatch);
  result=query(u,in,out,"AT+QURCCFG=\"urcport\"",all,sizeof(all));if(result)goto end;
  if(!strstr(all,"\"usbat\"")&&!strstr(all,"\"all\"")){result=23;goto end;}
  result=query(u,in,out,"AT+CNMI?",all,sizeof(all));if(result)goto end;
  unsigned a,b,c,d,e;char *cn=strstr(all,"+CNMI:");if(!cn||sscanf(cn,"+CNMI: %u,%u,%u,%u,%u",&a,&b,&c,&d,&e)!=5){result=23;goto end;}
  snprintf(oldcnmi,sizeof(oldcnmi),"AT+CNMI=%u,%u,%u,%u,%u",a,b,c,d,e);
  result=query(u,in,out,"AT+CNMI=2,1,0,0,0",all,sizeof(all));if(result)goto end;
  int pending=strstr(all,"+CMTI:")!=NULL;
  result=query(u,in,out,"AT+CNMI?",all,sizeof(all));if(result)goto end;pending|=strstr(all,"+CMTI:")!=NULL;
  cn=strstr(all,"+CNMI:");if(!cn||sscanf(cn,"+CNMI: %u,%u,%u,%u,%u",&a,&b,&c,&d,&e)!=5||a!=2||b!=1||c||d||e){result=23;goto end;}
  puts("READY");fflush(stdout);fcntl(STDIN_FILENO,F_SETFL,O_NONBLOCK);
  char line[4096];size_t used=0;
  while(!stopping){
   if(pending){puts("EVENT");fflush(stdout);goto end;}
   char control;ssize_t n=read(STDIN_FILENO,&control,1);if(n==0){goto end;}if(n>0){puts("MANUAL");fflush(stdout);goto end;}
   char buf[512];UInt32 len=sizeof(buf);IOReturn rr=(*u)->ReadPipeTO(u,in,buf,&len,500,1000);
   if(rr==kIOReturnTimeout||rr==kIOUSBTransactionTimeout)continue;if(rr){fprintf(stderr,"watch USB error %x\n",rr);result=24;goto end;}
   for(UInt32 k=0;k<len;k++){if(buf[k]=='\r')continue;if(buf[k]=='\n'){line[used]=0;if(strstr(line,"+CMTI:")==line)pending=1;used=0;}else if(used<sizeof(line)-1)line[used++]=buf[k];else{result=25;goto end;}}
  }
  goto end;
 }
 char select[80];snprintf(select,sizeof(select),"AT+CPMS=\"%s\"",store);changed=1;result=query(u,in,out,select,all,sizeof(all));if(result)goto end;
 result=query(u,in,out,"AT+CPMS?",all,sizeof(all));if(result)goto end;char actual[8];char *cp=strstr(all,"+CPMS:");if(!cp||sscanf(cp,"+CPMS: \"%7[A-Z]\"",actual)!=1||strcmp(actual,store)){result=14;goto end;}
 if(!deleting)printf("STORAGE %s\n%s\n",store,all);
 if(deleting){
  char cmd[80];snprintf(cmd,sizeof(cmd),"AT+CMGR=%u",slot);result=query(u,in,out,cmd,all,sizeof(all));if(result)goto end;
  if(!same_pdu(all,expected)){result=21;goto end;}
  snprintf(cmd,sizeof(cmd),"AT+CMGD=%u,0",slot);result=query(u,in,out,cmd,all,sizeof(all));if(!result)puts("DELETED");
 }else{
  result=query(u,in,out,"AT+CMGL=4",all,sizeof(all));if(!result)printf("QUERY AT+CMGL=4\n%s\n",all);
 }
end:
 if(oldcnmi[0]){int rr=query(u,in,out,oldcnmi,all,sizeof(all));if(!result&&rr)result=rr;}
 if(changed&&original[0]){char restore[80];snprintf(restore,sizeof(restore),"AT+CPMS=\"%s\"",original);int rr=query(u,in,out,restore,all,sizeof(all));if(!result&&rr)result=rr;}
 (*u)->USBInterfaceClose(u);(*u)->Release(u);return result;
}
